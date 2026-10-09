package session

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Minimal destination fixture for narrow metadata writes. Native tests also
// qualify the real migration fingerprint, full schema and timestamp triggers.
func promotionDB(t *testing.T, home *Home, id, selected string) *sql.DB {
	t.Helper()
	db := fixtureDB(t, home, true)
	for _, statement := range []string{
		"ALTER TABLE threads ADD COLUMN archived_at INTEGER",
		"ALTER TABLE threads ADD COLUMN title TEXT NOT NULL DEFAULT 'keep my title'",
		"ALTER TABLE threads ADD COLUMN updated_at INTEGER NOT NULL DEFAULT 123456",
		"CREATE TABLE _sqlx_migrations(version INTEGER PRIMARY KEY, checksum BLOB NOT NULL, success INTEGER NOT NULL)",
		"CREATE TABLE backfill_state(id INTEGER PRIMARY KEY, status TEXT NOT NULL)",
		"INSERT INTO backfill_state VALUES (1, 'complete')",
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	checksum, err := hex.DecodeString(selectionMigrations[58])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO _sqlx_migrations VALUES (58, ?, 1)", checksum); err != nil {
		t.Fatal(err)
	}
	if selected != "" {
		if _, err := db.Exec("INSERT INTO threads(id, rollout_path, archived, history_mode, archived_at) VALUES (?, ?, 1, 'paginated', 111)", id, filepath.Join(home.Path, selected)); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestParentPromotionPreservesMetadataAndSupportsRetry(t *testing.T) {
	for _, tc := range []struct {
		version     int
		interrupted bool
	}{{58, false}, {58, true}, {59, false}, {59, true}} {
		t.Run(fmt.Sprintf("migration=%d/interrupted=%v", tc.version, tc.interrupted), func(t *testing.T) {
			src, dst := fixtureHome(t), fixtureHome(t)
			parent, child, prefix := fixtureFork(t, src)
			first := published(t, received(t, dst, exported(t, src, threadB)))
			db := promotionDB(t, dst, threadA, first.Files[0].Path)
			setPromotionMigration(t, db, tc.version)
			other := fixturePath(threadC, threadC)
			writeFixture(t, dst, other, fixtureBytes(t, threadC, "paginated", nil, "unrelated"))
			if _, err := db.Exec("INSERT INTO threads(id, rollout_path, archived, history_mode) VALUES (?, ?, 0, 'paginated')", threadC, other); err != nil {
				t.Fatal(err)
			}
			stream := exported(t, src, threadA)
			in := received(t, dst, stream)
			if tc.interrupted {
				stop := errors.New("interrupted before metadata commit")
				if _, err := in.publish(context.Background(), dst, func(int) error { return stop }); !errors.Is(err, stop) {
					t.Fatalf("unexpected interruption: %v", err)
				}
				before, err := dst.selection(context.Background(), threadA)
				if err != nil || before == nil || !strings.Contains(before.path, prerequisiteRoot) {
					t.Fatalf("failed publication changed metadata: %+v %v", before, err)
				}
				if err := in.Close(); err != nil {
					t.Fatal(err)
				}
				in = received(t, dst, stream)
			}
			result := published(t, in)
			if !result.Files[0].Promoted || !result.SelectionRepaired {
				t.Fatalf("promotion not reported: %+v", result)
			}
			var title string
			var updated int64
			var archivedAt sql.NullInt64
			if err := db.QueryRow("SELECT title, updated_at, archived_at FROM threads WHERE id = ?", threadA).Scan(&title, &updated, &archivedAt); err != nil || title != "keep my title" || updated != 123456 || archivedAt.Valid {
				t.Fatalf("promotion changed unrelated metadata or retained stale archive time: %q %d %+v %v", title, updated, archivedAt, err)
			}
			unrelated, err := dst.selection(context.Background(), threadC)
			if err != nil || unrelated == nil || unrelated.path != other || unrelated.archived {
				t.Fatalf("promotion changed unrelated row: %+v %v", unrelated, err)
			}
			if got := inspect(t, dst, threadA); len(got.Files) != 1 || got.Files[0].SHA256 != hash(parent) {
				t.Fatal("complete promoted parent differs from source")
			}
			if got := inspect(t, dst, threadB); got.Files[0].SHA256 != hash(prefix) || got.Files[1].SHA256 != hash(child) {
				t.Fatal("promotion changed existing child's history")
			}
		})
	}
}

func setPromotionMigration(t *testing.T, db *sql.DB, version int) {
	t.Helper()
	checksum, err := hex.DecodeString(selectionMigrations[version])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE _sqlx_migrations SET version = ?, checksum = ?", version, checksum); err != nil {
		t.Fatal(err)
	}
}

func TestParentPromotionRejectsUnsupportedMetadataBeforePublication(t *testing.T) {
	for _, mutation := range []string{
		"UPDATE _sqlx_migrations SET version = 60",
		"UPDATE _sqlx_migrations SET version = 59",
		"UPDATE _sqlx_migrations SET checksum = X'00'",
		"UPDATE _sqlx_migrations SET success = 0",
		"UPDATE backfill_state SET status = 'running'",
		"CREATE TRIGGER unexpected AFTER UPDATE ON threads BEGIN DELETE FROM threads WHERE id <> new.id; END",
		"ALTER TABLE threads RENAME COLUMN archived_at TO incompatible",
	} {
		t.Run(mutation, func(t *testing.T) {
			src, dst := fixtureHome(t), fixtureHome(t)
			_, _, prefix := fixtureFork(t, src)
			first := published(t, received(t, dst, exported(t, src, threadB)))
			db := promotionDB(t, dst, threadA, first.Files[0].Path)
			if _, err := db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			if _, err := received(t, dst, exported(t, src, threadA)).Publish(context.Background(), dst); err == nil {
				t.Fatal("accepted unqualified metadata update")
			}
			if got, err := os.ReadFile(filepath.Join(dst.Path, first.Files[0].Path)); err != nil || !bytes.Equal(got, prefix) {
				t.Fatal("schema refusal changed the prerequisite")
			}
			if _, err := os.Stat(filepath.Join(dst.Path, fixturePath(threadA, threadA))); !os.IsNotExist(err) {
				t.Fatal("schema refusal published a complete parent")
			}
		})
	}
}

func TestParentPromotionRejectsConflictingContentBeforeWrites(t *testing.T) {
	for _, kind := range []string{"divergent", "shorter", "another selected rollout"} {
		t.Run(kind, func(t *testing.T) {
			src, dst := fixtureHome(t), fixtureHome(t)
			short, longer := fixtureTwoForks(t, src)
			first := published(t, received(t, dst, exported(t, src, threadC)))
			before, _ := os.Stat(filepath.Join(dst.Path, first.Files[0].Path))
			switch kind {
			case "divergent":
				writeFixture(t, src, fixturePath(threadA, threadA), fixtureBytes(t, threadA, "paginated", nil, "different"))
			case "shorter":
				writeFixture(t, src, fixturePath(threadA, threadA), short)
			case "another selected rollout":
				path := fixturePath(threadA, rolloutA)
				writeFixture(t, dst, path, fixtureBytes(t, threadA, "paginated", nil, "unrelated revert"))
				promotionDB(t, dst, threadA, path)
			}
			if _, err := received(t, dst, exported(t, src, threadA)).Publish(context.Background(), dst); !errors.Is(err, ErrConflict) {
				t.Fatalf("expected conflict: %v", err)
			}
			after, _ := os.Stat(filepath.Join(dst.Path, first.Files[0].Path))
			data, _ := os.ReadFile(filepath.Join(dst.Path, first.Files[0].Path))
			if !os.SameFile(before, after) || !bytes.Equal(data, longer) {
				t.Fatal("conflicting promotion changed dependent history")
			}
		})
	}
}

func TestPublicationWaitsForRunningIndexerAndHonorsCancellation(t *testing.T) {
	for _, cancelWait := range []bool{false, true} {
		t.Run(map[bool]string{true: "cancel", false: "complete"}[cancelWait], func(t *testing.T) {
			src, dst := fixtureHome(t), fixtureHome(t)
			id, _ := nativeParentAndChild(t, src, true, false)
			in := received(t, dst, exported(t, src, id))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var db *sql.DB
			done := make(chan error, 1)
			result, err := in.publish(ctx, dst, func(i int) error {
				if i == 0 {
					db = promotionDB(t, dst, id, in.Plan.Files[0].Path)
					if _, err := db.Exec("UPDATE backfill_state SET status = 'running'"); err != nil {
						t.Fatal(err)
					}
					return nil
				}
				go func() {
					time.Sleep(75 * time.Millisecond)
					guard := &writerGuards{home: dst, files: make(map[string]*os.File)}
					defer guard.Close()
					if err := guard.acquire(id); !errors.Is(err, ErrBusy) {
						done <- errors.New("publication released writer guards before indexing completed")
						cancel()
						return
					}
					if cancelWait {
						cancel()
						done <- nil
						return
					}
					_, err := db.Exec("UPDATE backfill_state SET status = 'complete'")
					done <- err
				}()
				return nil
			})
			if workerErr := <-done; workerErr != nil {
				t.Fatal(workerErr)
			}
			if cancelWait {
				if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "history files are installed") {
					t.Fatalf("canceled verification lost publication information: %v", err)
				}
				if _, err := db.Exec("UPDATE backfill_state SET status = 'complete'"); err != nil {
					t.Fatal(err)
				}
				result = published(t, in)
			} else if err != nil {
				t.Fatal(err)
			}
			if !result.SelectionRepaired {
				t.Fatal("overlapping index was not repaired")
			}
		})
	}
}
