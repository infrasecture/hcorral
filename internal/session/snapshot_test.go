package session

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotPinsSavedBytesAcrossSourceChanges(t *testing.T) {
	for _, change := range []string{"append", "archive", "replace", "delete"} {
		for _, compressed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/compressed=%v", change, compressed), func(t *testing.T) {
				src, dst := fixtureHome(t), fixtureHome(t)
				data := fixtureBytes(t, threadA, "paginated", nil, "saved before copy")
				path := fixturePath(threadA, threadA)
				if compressed {
					path += ".zst"
				}
				writeFixture(t, src, path, data)
				snapshot, err := src.Snapshot(context.Background(), threadA, src, DefaultLimits())
				if err != nil {
					t.Fatal(err)
				}
				defer snapshot.Close()
				if _, err := os.Stat(filepath.Join(src.Path, writerDirectory)); !os.IsNotExist(err) {
					t.Fatal("source snapshot created writer locks")
				}
				full := filepath.Join(src.Path, path)
				switch change {
				case "append":
					f, err := os.OpenFile(full, os.O_APPEND|os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					_, err = f.WriteString("later data must not enter the snapshot")
					f.Close()
					if err != nil {
						t.Fatal(err)
					}
				case "archive", "replace":
					archive := filepath.Join(src.Path, "archived_sessions")
					if err := os.MkdirAll(archive, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(full, filepath.Join(archive, filepath.Base(full))); err != nil {
						t.Fatal(err)
					}
					if change == "replace" {
						writeFixture(t, src, path, fixtureBytes(t, threadA, "paginated", nil, "new representation"))
					}
				case "delete":
					if err := os.Remove(full); err != nil {
						t.Fatal(err)
					}
				}
				var wire bytes.Buffer
				if err := snapshot.Export(context.Background(), &wire); err != nil {
					t.Fatal(err)
				}
				result := published(t, received(t, dst, wire.Bytes()))
				got, err := os.ReadFile(filepath.Join(dst.Path, result.MainPath))
				if err != nil || !bytes.Equal(got, data) {
					t.Fatalf("snapshot did not preserve its captured history: %v", err)
				}
				if err := snapshot.Close(); err != nil {
					t.Fatal(err)
				}
				if err := snapshot.Export(context.Background(), &wire); err == nil {
					t.Fatal("exported a closed snapshot")
				}
			})
		}
	}
}

func TestSnapshotExcludesOnlyAnUnfinishedFinalRecord(t *testing.T) {
	for _, tail := range []string{"{", "{\"type\":\"response_item\",\"payload\":", strings.Repeat("x", 70<<10)} {
		t.Run(fmt.Sprint(len(tail)), func(t *testing.T) {
			src, dst := fixtureHome(t), fixtureHome(t)
			data := fixtureBytes(t, threadA, "paginated", nil, "complete saved message")
			writeFixture(t, src, fixturePath(threadA, threadA), append(append([]byte(nil), data...), tail...))
			result := published(t, received(t, dst, exported(t, src, threadA)))
			got, err := os.ReadFile(filepath.Join(dst.Path, result.MainPath))
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("copied an unfinished record or dropped saved records: %v", err)
			}
		})
	}
	for _, tail := range []string{"malformed\n", "{\"type\":\"response_item\"}\n", strings.Repeat("x", 2049)} {
		src := fixtureHome(t)
		data := fixtureBytes(t, threadA, "paginated", nil, "saved")
		writeFixture(t, src, fixturePath(threadA, threadA), append(data, tail...))
		limits := DefaultLimits()
		limits.RecordBytes = 2048
		if snapshot, err := src.Snapshot(context.Background(), threadA, src, limits); err == nil {
			snapshot.Close()
			t.Fatal("accepted malformed complete record or oversized unfinished record")
		}
	}
}

func TestSnapshotRejectsChangedSQLiteSelection(t *testing.T) {
	h := fixtureHome(t)
	path := fixturePath(threadA, threadA)
	writeFixture(t, h, path, fixtureBytes(t, threadA, "paginated", nil, "selected"))
	db := fixtureDB(t, h, true)
	defer db.Close()
	if _, err := db.Exec("INSERT INTO threads VALUES (?, ?, 0, 'paginated')", threadA, filepath.Join(h.Path, path)); err != nil {
		t.Fatal(err)
	}
	_, err := h.inspect(context.Background(), threadA, h, DefaultLimits(), func(c candidate, end *HistoryPosition) (File, error) {
		file, err := h.readRollout(context.Background(), c, end, DefaultLimits())
		if err != nil {
			return File{}, err
		}
		_, err = db.Exec("UPDATE threads SET rollout_path = ? WHERE id = ?", filepath.Join(h.Path, fixturePath(threadA, rolloutA)), threadA)
		return file, err
	})
	if err == nil || !strings.Contains(err.Error(), "selection changed") {
		t.Fatalf("accepted a changed authoritative selection: %v", err)
	}
}
