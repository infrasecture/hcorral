package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

type selection struct {
	path, mode string
	archived   bool
}

func isMissing(err error) bool { return errors.Is(err, os.ErrNotExist) }

// selection uses a real read-only SQLite transaction, including committed WAL
// records. immutable=1 is deliberately absent: it ignores a live database's WAL.
// The caller must separately coordinate Codex writers for a consistent copy of
// both the selected database metadata and the files it references.
func (h *Home) selection(ctx context.Context, id string) (*selection, error) {
	const name = "state_5.sqlite"
	entries, err := os.ReadDir(h.Path)
	if err != nil {
		return nil, err
	}
	var oldDatabase string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "state_") && strings.HasSuffix(entry.Name(), ".sqlite") && entry.Name() != name {
			version, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(entry.Name(), "state_"), ".sqlite"))
			if err != nil || version > 5 {
				return nil, fmt.Errorf("unsupported or ambiguous Codex state database %s; qualify its schema before transferring sessions", entry.Name())
			}
			oldDatabase = entry.Name()
		}
	}
	f, err := h.regular(name)
	if isMissing(err) {
		if oldDatabase != "" {
			return nil, fmt.Errorf("unsupported Codex state database %s; refusing to ignore its thread selection", oldDatabase)
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, err
	}
	// The SQLite driver opens its own descriptors. Refuse symlink/special
	// database sidecars rather than exposing those paths to the native VFS.
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		file, err := h.regular(name + suffix)
		if err == nil {
			file.Close()
		} else if !isMissing(err) {
			return nil, err
		}
	}
	path := filepath.Join(h.Path, name)
	uri := url.URL{Scheme: "file", Path: path}
	query := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(1000)", "trusted_schema(0)"}}
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("read Codex state database: %w", err)
	}
	defer tx.Rollback()
	// Older state_5 schemas predate history_mode. They describe legacy
	// rollouts; do not require an unrelated Codex schema migration to read them.
	rows, err := tx.QueryContext(ctx, "SELECT name FROM pragma_table_info('threads')")
	if err != nil {
		return nil, fmt.Errorf("inspect Codex thread schema: %w", err)
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			rows.Close()
			return nil, err
		}
		columns[column] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, column := range []string{"id", "rollout_path", "archived"} {
		if !columns[column] {
			return nil, fmt.Errorf("unsupported Codex thread schema: missing %s", column)
		}
	}
	modeColumn := "'legacy'"
	if columns["history_mode"] {
		modeColumn = "history_mode"
	}
	var result selection
	err = tx.QueryRowContext(ctx, "SELECT rollout_path, "+modeColumn+", archived FROM threads WHERE id = ?", id).Scan(&result.path, &result.mode, &result.archived)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("read selected rollout for %s: %w", id, err)
	}
	// Catch replacement of the checked path before or during the query. All
	// conversation-file I/O itself remains rooted at h's directory descriptor.
	after, statErr := os.Lstat(path)
	if statErr != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, fmt.Errorf("Codex state database changed identity during inspection")
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if result.mode != "legacy" && result.mode != "paginated" {
		return nil, fmt.Errorf("unsupported SQLite history mode %q", result.mode)
	}
	return &result, nil
}
