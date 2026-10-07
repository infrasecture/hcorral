package session

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Metadata writes are deliberately narrower than read-only format support.
// This adapter targets Codex 0.160.0/0.160.1, state_5 migration 58.
// A later schema requires qualification; no migrations or bulk row imports run.
const selectionMigrationChecksum = "68467af3ce1ff09777c3f7167d522e11d88586fbb80a1588c631cf643a7d4985b221a6c8e8623997b37d44605b7e1091"

// Hash whitespace-normalized sqlite_schema SQL, without the final semicolon.
// These native timestamp triggers do not fire for our path/archive-only update.
// Refuse unknown or changed triggers rather than execute unexpected side effects.
var selectionTriggers = map[string]string{
	"threads_created_at_ms_after_insert": "586126949b44728f1aceff4a390164913f5906a01111a2c8dad855287342b511",
	"threads_updated_at_ms_after_insert": "6f880f1cc9191fe60877a7cbb2b1538ea4b2d7b9dfacd2992f03f4795b8ba800",
	"threads_created_at_ms_after_update": "5a54826b36daa1b4451714ce187b238116390a16a53e513cc30074c1b4fc3611",
	"threads_updated_at_ms_after_update": "9de2de92b3540251ab93ca8bf7041dff312084a5e1174bf0dc4eb41bb3faaf65",
	"threads_recency_at_after_insert":    "44e9dd026465a1ebdcfcb53f3a87f35cf860af73d37f5dde2c0a29b55dab9650",
}

type selectionUpdate struct {
	state *stateDB
	tx    *sql.Tx
	id    string
	old   selection
}

func (u *selectionUpdate) close() {
	if u.tx != nil {
		u.tx.Rollback()
	}
	u.state.close()
}

// Acquire the database write reservation only after slow history validation and
// staging. The caller still holds all native thread/rollout writer guards. No
// live file may be published until this reservation validates the expected row.
func (h *Home) beginSelectionUpdate(ctx context.Context, id string, expected selection) (_ *selectionUpdate, resultErr error) {
	state, err := h.openStateDB(true)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, conflict("destination metadata disappeared before prerequisite promotion")
	}
	u := &selectionUpdate{state: state, id: id, old: expected}
	defer func() {
		if resultErr != nil {
			u.close()
		}
	}()
	u.tx, err = state.db.BeginTx(ctx, nil) // DSN requires BEGIN IMMEDIATE.
	if err != nil {
		return nil, fmt.Errorf("reserve destination metadata for prerequisite promotion: %w", err)
	}
	if err := validateSelectionSchema(ctx, u.tx); err != nil {
		return nil, err
	}
	actual, err := readSelection(ctx, u.tx, id)
	if err != nil {
		return nil, err
	}
	if actual == nil || *actual != expected || actual.mode != "paginated" {
		return nil, conflict("destination selected rollout changed before prerequisite promotion")
	}
	if err := state.verifyIdentity(); err != nil {
		return nil, err
	}
	return u, nil
}

func validateSelectionSchema(ctx context.Context, tx *sql.Tx) error {
	var version int
	var checksum []byte
	var success bool
	if err := tx.QueryRowContext(ctx, "SELECT version, checksum, success FROM _sqlx_migrations ORDER BY version DESC LIMIT 1").Scan(&version, &checksum, &success); err != nil || version != 58 || !success || hex.EncodeToString(checksum) != selectionMigrationChecksum {
		return errors.New("destination metadata promotion requires the qualified state_5 migration 58 layout")
	}
	var status string
	if err := tx.QueryRowContext(ctx, "SELECT status FROM backfill_state WHERE id = 1").Scan(&status); err != nil || status != "complete" {
		return fmt.Errorf("%w: destination initial indexing must finish before prerequisite promotion", ErrBusy)
	}
	rows, err := tx.QueryContext(ctx, "SELECT name, type, pk FROM pragma_table_info('threads')")
	if err != nil {
		return err
	}
	required := map[string]string{"id": "TEXT", "rollout_path": "TEXT", "history_mode": "TEXT", "archived": "INTEGER", "archived_at": "INTEGER"}
	primaryKeys := 0
	for rows.Next() {
		var name, kind string
		var primary int
		if err := rows.Scan(&name, &kind, &primary); err != nil {
			rows.Close()
			return err
		}
		if primary > 0 {
			primaryKeys++
			if name != "id" {
				rows.Close()
				return errors.New("unsupported destination thread primary key")
			}
		}
		if want, present := required[name]; present && strings.EqualFold(kind, want) {
			delete(required, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(required) != 0 || primaryKeys != 1 {
		return errors.New("unsupported destination thread columns for metadata promotion")
	}
	rows, err = tx.QueryContext(ctx, "SELECT name, sql FROM sqlite_schema WHERE type = 'trigger' AND tbl_name = 'threads'")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name, statement string
		if err := rows.Scan(&name, &statement); err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(strings.Join(strings.Fields(strings.TrimSuffix(strings.TrimSpace(statement), ";")), " ")))
		if selectionTriggers[name] != hex.EncodeToString(digest[:]) {
			return fmt.Errorf("unsupported destination thread trigger %q; metadata was not changed", name)
		}
	}
	return rows.Err()
}

// All live history must be durable before this call. On any error the caller
// retains those complete files: a commit error does not prove that SQLite stayed
// unchanged. A retry verifies the files and the actual selected path again.
func (u *selectionUpdate) finish(ctx context.Context, path string, archived bool, modified time.Time) error {
	if err := u.state.verifyIdentity(); err != nil {
		return err
	}
	result, err := u.tx.ExecContext(ctx, `UPDATE threads
SET rollout_path = ?, archived = ?, archived_at = CASE WHEN ? THEN COALESCE(archived_at, ?) ELSE NULL END
WHERE id = ? AND rollout_path = ? AND history_mode = ? AND archived = ?`,
		path, archived, archived, modified.Unix(), u.id, u.old.path, u.old.mode, u.old.archived)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return conflict("destination metadata no longer selects the validated prerequisite")
	}
	if err := u.state.verifyIdentity(); err != nil {
		return err
	}
	return u.tx.Commit()
}

// Native backfill does not participate in the thread writer locks. It commits
// 'running' BEFORE collecting rollout paths, then 'complete' after its upserts.
// Observe its state and selection in ONE read transaction after every file is
// durable. A scan that has not started yet will see the complete publication;
// an overlapping scan must finish before its selected prerequisite is repaired.
func (h *Home) publicationSelection(ctx context.Context, id string) (string, *selection, error) {
	state, err := h.openStateDB(false)
	if err != nil || state == nil {
		return "absent", nil, err
	}
	defer state.close()
	tx, err := state.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", nil, err
	}
	defer tx.Rollback()
	var tables int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name IN ('threads', 'backfill_state', '_sqlx_migrations')").Scan(&tables); err != nil {
		return "", nil, err
	}
	if tables != 3 {
		return "", nil, fmt.Errorf("%w: destination indexing schema is not initialized", ErrBusy)
	}
	var version int
	var checksum []byte
	var success bool
	if err := tx.QueryRowContext(ctx, "SELECT version, checksum, success FROM _sqlx_migrations ORDER BY version DESC LIMIT 1").Scan(&version, &checksum, &success); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, fmt.Errorf("%w: destination schema migrations have not completed", ErrBusy)
		}
		return "", nil, err
	}
	if version < 58 && success {
		return "", nil, fmt.Errorf("%w: destination schema migrations have not reached the qualified layout", ErrBusy)
	}
	if version != 58 || !success || hex.EncodeToString(checksum) != selectionMigrationChecksum {
		return "", nil, errors.New("cannot confirm destination indexing against an unqualified state_5 schema")
	}
	status := "pending"
	err = tx.QueryRowContext(ctx, "SELECT status FROM backfill_state WHERE id = 1").Scan(&status)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", nil, err
	}
	if status != "pending" && status != "running" && status != "complete" {
		return "", nil, fmt.Errorf("unsupported destination indexing status %q", status)
	}
	selected, err := readSelection(ctx, tx, id)
	if err != nil {
		return "", nil, err
	}
	if err := state.verifyIdentity(); err != nil {
		return "", nil, err
	}
	return status, selected, nil
}

func (in *Incoming) finishPublicationSelection(ctx context.Context, home *Home, result Result) (bool, error) {
	selected, err := in.awaitPublicationSelection(ctx, home)
	if err != nil || selected == nil {
		return false, err // No row means native filesystem fallback; no repair.
	}
	path, err := in.home.managedRelative(selected.path)
	if err != nil {
		return false, err
	}
	if !prerequisitePath(path) {
		if strings.TrimSuffix(path, ".zst") != strings.TrimSuffix(result.MainPath, ".zst") || selected.mode != result.Metadata.HistoryMode || selected.archived != result.Archived {
			return false, conflict("destination selected a different complete conversation during publication")
		}
		return false, nil
	}
	// Revalidate bytes and identities before admitting a row first observed
	// after publication. Never repair an unrelated rollout.
	choices, err := in.checkConflicts(ctx, home)
	if err != nil {
		return false, err
	}
	expected := choices[len(choices)-1].repair
	if expected == nil || *expected != *selected {
		return false, conflict("destination selection changed during index verification")
	}
	update, err := home.beginSelectionUpdate(ctx, in.Plan.ThreadID, *expected)
	if err != nil {
		return false, err
	}
	defer update.close()
	main := in.Plan.Files[len(in.Plan.Files)-1]
	if err := update.finish(ctx, filepath.Join(in.home.Path, result.MainPath), result.Archived, main.Modified); err != nil {
		return false, err
	}
	return true, nil
}

func (in *Incoming) awaitPublicationSelection(ctx context.Context, home *Home) (*selection, error) {
	// Bound only index readiness, not validation of potentially large histories.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	for {
		status, selected, err := home.publicationSelection(ctx, in.Plan.ThreadID)
		wait := errors.Is(err, ErrBusy) || sqliteBusy(err)
		if err != nil && !wait {
			return nil, err
		}
		if err == nil {
			wait = status == "running"
			if status == "pending" && selected != nil {
				path, err := in.home.managedRelative(selected.path)
				if err != nil {
					return nil, err
				}
				wait = prerequisitePath(path)
			}
			if !wait {
				return selected, nil
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("waiting for destination initial indexing: %w", err)
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("waiting for destination initial indexing: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func sqliteBusy(err error) bool {
	var coded interface{ Code() int }
	return errors.As(err, &coded) && (coded.Code()&255 == 5 || coded.Code()&255 == 6)
}
