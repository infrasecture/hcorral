package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
)

const (
	threadA  = "019a1234-1111-7111-8111-111111111111"
	threadB  = "019a1234-2222-7222-8222-222222222222"
	threadC  = "019a1234-3333-7333-8333-333333333333"
	rolloutA = "019a1234-4444-7444-8444-444444444444"
)

func fixtureHome(t *testing.T) *Home {
	t.Helper()
	h, err := OpenHome(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

func fixturePath(thread, rollout string) string {
	ids := thread
	if rollout != thread {
		ids += "_" + rollout
	}
	return "sessions/2026/10/06/rollout-2026-10-06T12-34-56-" + ids + ".jsonl"
}

func fixtureBytes(t *testing.T, id, mode string, base *HistoryPosition, texts ...string) []byte {
	t.Helper()
	meta := Metadata{ThreadID: id, Version: "0.160.0", CWD: "/workspace with spaces", HistoryMode: mode, HistoryBase: base}
	initial := uint64(0)
	if base != nil {
		initial = base.EndOrdinalExclusive
	}
	var b bytes.Buffer
	for i := -1; i < len(texts); i++ {
		record := map[string]any{"timestamp": "2026-10-06T12:34:56Z", "type": "session_meta", "payload": meta}
		if i == -1 {
			encoded, err := json.Marshal(meta)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(encoded, &payload); err != nil {
				t.Fatal(err)
			}
			payload["session_id"] = id
			payload["timestamp"] = "2026-10-06T12:34:56Z"
			payload["originator"] = "hcorral_test"
			payload["source"] = "cli"
			payload["model_provider"] = "test-provider"
			record["payload"] = payload
		}
		if i >= 0 {
			record["type"] = "response_item"
			record["payload"] = map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": texts[i]}}}
		}
		if mode == "paginated" {
			record["ordinal"] = initial + uint64(i+1)
		}
		if err := json.NewEncoder(&b).Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	return b.Bytes()
}

func writeFixture(t *testing.T, h *Home, path string, data []byte) {
	t.Helper()
	full := filepath.Join(h.Path, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if strings.HasSuffix(path, ".zst") {
		encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
		if err != nil {
			t.Fatal(err)
		}
		data = encoder.EncodeAll(data, nil)
		encoder.Close()
	}
	if err := os.WriteFile(full, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func inspect(t *testing.T, h *Home, id string) Plan {
	t.Helper()
	plan, err := h.Inspect(context.Background(), id, h, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func TestInspectPreservesBytesAndLargeOpaqueRecords(t *testing.T) {
	for _, mode := range []string{"legacy", "paginated"} {
		for _, compressed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/compressed=%v", mode, compressed), func(t *testing.T) {
				h := fixtureHome(t)
				data := fixtureBytes(t, threadA, mode, nil, strings.Repeat("large unknown payload ☃\n", 5000))
				// Preserve spacing and fields our metadata model never interprets.
				data = bytes.Replace(data, []byte(`"cli_version"`), []byte(`"future_field": {"opaque": [1, true, "secret-shaped-data"]}, "cli_version"`), 1)
				path := fixturePath(threadA, threadA)
				if compressed {
					path += ".zst"
				}
				writeFixture(t, h, path, data)
				plan := inspect(t, h, strings.ToUpper(threadA))
				if len(plan.Files) != 1 || plan.Files[0].SHA256 != hash(data) || plan.Files[0].Bytes != int64(len(data)) {
					t.Fatalf("unexpected plan: %+v", plan)
				}
				if strings.HasSuffix(plan.Files[0].Path, ".zst") || plan.Files[0].Prefix {
					t.Fatal("incorrect output representation")
				}
			})
		}
	}
}

func TestInspectNestedInheritedPrefixesByRolloutID(t *testing.T) {
	h := fixtureHome(t)
	rootPrefix := fixtureBytes(t, threadA, "paginated", nil, "inherited root")
	rootFull := fixtureBytes(t, threadA, "paginated", nil, "inherited root", "private root continuation")
	rootPath := "archived_sessions/" + filepath.Base(fixturePath(threadA, rolloutA)) + ".zst"
	writeFixture(t, h, rootPath, rootFull)
	base := &HistoryPosition{RolloutID: rolloutA, EndOrdinalExclusive: 2, EndByteOffset: uint64(len(rootPrefix))}
	middlePrefix := fixtureBytes(t, threadB, "paginated", base)
	middleFull := fixtureBytes(t, threadB, "paginated", base, "private middle continuation")
	writeFixture(t, h, fixturePath(threadB, threadB), middleFull)
	childBase := &HistoryPosition{RolloutID: threadB, EndOrdinalExclusive: 3, EndByteOffset: uint64(len(middlePrefix))}
	child := fixtureBytes(t, threadC, "paginated", childBase, "child message")
	writeFixture(t, h, fixturePath(threadC, threadC), child)
	plan := inspect(t, h, threadC)
	if len(plan.Files) != 3 {
		t.Fatalf("got %d files", len(plan.Files))
	}
	for i, want := range [][]byte{rootPrefix, middlePrefix, child} {
		if plan.Files[i].SHA256 != hash(want) || plan.Files[i].Bytes != int64(len(want)) || plan.Files[i].Prefix != (i < 2) {
			t.Fatalf("incorrect segment %d: %+v", i, plan.Files[i])
		}
	}
	if plan.Files[0].RolloutID != rolloutA || plan.Files[0].ThreadID != threadA {
		t.Fatal("confused rollout and thread identity")
	}
}

func fixtureDB(t *testing.T, h *Home, hasMode bool) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(h.Path, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	schema := "CREATE TABLE threads(id TEXT PRIMARY KEY, rollout_path TEXT NOT NULL, archived INTEGER NOT NULL"
	if hasMode {
		schema += ", history_mode TEXT NOT NULL"
	}
	schema += ")"
	for _, statement := range []string{"PRAGMA journal_mode=WAL", "PRAGMA wal_autocheckpoint=0", schema} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestSQLiteSelectionIncludesCommittedWAL(t *testing.T) {
	h := fixtureHome(t)
	db := fixtureDB(t, h, true)
	oldPath, selectedPath := fixturePath(threadA, threadA), fixturePath(threadA, rolloutA)
	writeFixture(t, h, oldPath, fixtureBytes(t, threadA, "paginated", nil, "abandoned"))
	selected := fixtureBytes(t, threadA, "paginated", nil, "selected by revert")
	writeFixture(t, h, selectedPath+".zst", selected)
	if _, err := db.Exec("INSERT INTO threads VALUES (?, ?, 0, 'paginated')", threadA, filepath.Join(h.Path, selectedPath)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(h.Path, "state_5.sqlite-wal"))
	if err != nil || info.Size() == 0 {
		t.Fatal("test requires uncheckpointed WAL")
	}
	plan := inspect(t, h, threadA)
	if plan.Files[0].RolloutID != rolloutA || plan.Files[0].SHA256 != hash(selected) {
		t.Fatal("did not honor authoritative WAL selection")
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM threads").Scan(&count); err != nil || count != 1 {
		t.Fatal("inspection changed source metadata")
	}
}

func TestLegacySQLiteSchemaAndRelocatedDatabase(t *testing.T) {
	h, state := fixtureHome(t), fixtureHome(t)
	data := fixtureBytes(t, threadA, "legacy", nil, "legacy")
	path := "archived_sessions/old-custom-name.jsonl"
	writeFixture(t, h, path, data)
	db := fixtureDB(t, state, false)
	if _, err := db.Exec("INSERT INTO threads VALUES (?, ?, 1)", threadA, filepath.Join(h.Path, path)); err != nil {
		t.Fatal(err)
	}
	plan, err := h.Inspect(context.Background(), threadA, state, DefaultLimits())
	if err != nil || len(plan.Files) != 1 || plan.Files[0].SHA256 != hash(data) {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
}

func TestInspectRejectsAmbiguousAndConflictingRollouts(t *testing.T) {
	for _, differentID := range []bool{false, true} {
		t.Run(fmt.Sprint(differentID), func(t *testing.T) {
			h := fixtureHome(t)
			path := fixturePath(threadA, threadA)
			writeFixture(t, h, path, fixtureBytes(t, threadA, "paginated", nil, "one"))
			other := path + ".zst"
			if differentID {
				other = fixturePath(threadA, rolloutA)
			}
			writeFixture(t, h, other, fixtureBytes(t, threadA, "paginated", nil, "two"))
			_, err := h.Inspect(context.Background(), threadA, h, DefaultLimits())
			if err == nil {
				t.Fatal("accepted ambiguous/divergent rollout")
			}
		})
	}
	h := fixtureHome(t)
	data := fixtureBytes(t, threadA, "legacy", nil, "identical")
	writeFixture(t, h, fixturePath(threadA, threadA), data)
	writeFixture(t, h, fixturePath(threadA, threadA)+".zst", data)
	if len(inspect(t, h, threadA).Files) != 1 {
		t.Fatal("did not deduplicate equivalent representations")
	}
}

func TestInspectRejectsMalformedHistory(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func([]byte) []byte
		message string
	}{
		{"truncated", func(b []byte) []byte { return b[:len(b)-1] }, "incomplete"},
		{"invalid JSON", func(b []byte) []byte { return append(b, []byte("not-json\n")...) }, "invalid JSON"},
		{"wrong identity", func(b []byte) []byte { return bytes.ReplaceAll(b, []byte(threadA), []byte(threadB)) }, "identity"},
		{"unknown mode", func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("paginated"), []byte("future")) }, "unsupported"},
		{"wrong ordinal", func(b []byte) []byte { return bytes.ReplaceAll(b, []byte(`"ordinal":1`), []byte(`"ordinal":0`)) }, "non-increasing"},
		{"duplicate meta", func(b []byte) []byte { return append(b, bytes.SplitAfter(b, []byte("\n"))[0]...) }, "repeated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := fixtureHome(t)
			writeFixture(t, h, fixturePath(threadA, threadA), tc.mutate(fixtureBytes(t, threadA, "paginated", nil, "message")))
			_, err := h.Inspect(context.Background(), threadA, h, DefaultLimits())
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("got %v, want %s", err, tc.message)
			}
		})
	}
}

func TestInspectRejectsMissingAndInvalidPrefixes(t *testing.T) {
	for _, kind := range []string{"missing", "partial line", "wrong ordinal", "past EOF", "cycle"} {
		t.Run(kind, func(t *testing.T) {
			h := fixtureHome(t)
			parent := fixtureBytes(t, threadA, "paginated", nil, "parent")
			base := &HistoryPosition{RolloutID: threadA, EndOrdinalExclusive: 2, EndByteOffset: uint64(len(parent))}
			switch kind {
			case "partial line":
				base.EndByteOffset--
			case "wrong ordinal":
				base.EndOrdinalExclusive++
			case "past EOF":
				base.EndByteOffset++
			case "cycle":
				base.RolloutID = threadB
			}
			if kind != "missing" {
				writeFixture(t, h, fixturePath(threadA, threadA), parent)
			}
			writeFixture(t, h, fixturePath(threadB, threadB), fixtureBytes(t, threadB, "paginated", base, "child"))
			if _, err := h.Inspect(context.Background(), threadB, h, DefaultLimits()); err == nil {
				t.Fatal("accepted invalid dependency")
			}
		})
	}
}

func TestInspectRefusesSymlinksAndSpecialFiles(t *testing.T) {
	for _, kind := range []string{"root descendant", "file", "fifo", "db", "wal"} {
		t.Run(kind, func(t *testing.T) {
			h := fixtureHome(t)
			path := fixturePath(threadA, threadA)
			writeFixture(t, h, path, fixtureBytes(t, threadA, "legacy", nil, "safe"))
			switch kind {
			case "root descendant":
				if err := os.Symlink(t.TempDir(), filepath.Join(h.Path, "sessions", "escape")); err != nil {
					t.Fatal(err)
				}
			case "file", "fifo":
				full := filepath.Join(h.Path, path)
				if err := os.Remove(full); err != nil {
					t.Fatal(err)
				}
				if kind == "file" {
					if err := os.Symlink("/dev/null", full); err != nil {
						t.Fatal(err)
					}
				} else if err := unix.Mkfifo(full, 0o600); err != nil {
					t.Fatal(err)
				}
			case "db":
				if err := os.Symlink("/dev/null", filepath.Join(h.Path, "state_5.sqlite")); err != nil {
					t.Fatal(err)
				}
			case "wal":
				if err := os.WriteFile(filepath.Join(h.Path, "state_5.sqlite"), []byte("bad database"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/dev/null", filepath.Join(h.Path, "state_5.sqlite-wal")); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := h.Inspect(context.Background(), threadA, h, DefaultLimits()); err == nil {
				t.Fatal("accepted unsafe path")
			}
		})
	}
}

func TestHomeRootSymlinkAndCancellation(t *testing.T) {
	h := fixtureHome(t)
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(h.Path, link); err != nil {
		t.Fatal(err)
	}
	linked, err := OpenHome(link)
	if err != nil {
		t.Fatal(err)
	}
	defer linked.Close()
	if linked.Path != h.Path {
		t.Fatal("root was not resolved")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Inspect(ctx, threadA, h, DefaultLimits()); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	for _, id := range []string{"", "../../etc/passwd", "hcorral", threadA + ".jsonl"} {
		if _, err := ParseID(id); err == nil {
			t.Fatalf("accepted ID %q", id)
		}
	}
}

func TestSnapshotOwnsAllRequiredWriterLocks(t *testing.T) {
	h := fixtureHome(t)
	parent := fixtureBytes(t, threadA, "paginated", nil, "parent")
	writeFixture(t, h, fixturePath(threadA, rolloutA), parent)
	base := &HistoryPosition{RolloutID: rolloutA, EndOrdinalExclusive: 2, EndByteOffset: uint64(len(parent))}
	writeFixture(t, h, fixturePath(threadB, threadB), fixtureBytes(t, threadB, "paginated", base, "child"))
	snapshot, err := h.Snapshot(context.Background(), threadB, h, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	for _, id := range []string{threadA, threadB, rolloutA} {
		other := &writerGuards{home: h, files: make(map[string]*os.File)}
		if err := other.acquire(id); !errors.Is(err, ErrBusy) {
			t.Fatalf("%s was not owned: %v", id, err)
		}
		other.Close()
	}
	unrelated := &writerGuards{home: h, files: make(map[string]*os.File)}
	if err := unrelated.acquire(threadC); err != nil {
		t.Fatalf("blocked unrelated writer: %v", err)
	}
	unrelated.Close()
	snapshot.Close()
	second, err := h.Snapshot(context.Background(), threadB, h, DefaultLimits())
	if err != nil {
		t.Fatalf("stale lock entries must not prevent reuse: %v", err)
	}
	second.Close()
}

func TestSnapshotRefusesActiveSourceAndReleasesOnFailure(t *testing.T) {
	h := fixtureHome(t)
	writeFixture(t, h, fixturePath(threadA, threadA), fixtureBytes(t, threadA, "legacy", nil, "test"))
	writer := &writerGuards{home: h, files: make(map[string]*os.File)}
	if err := writer.acquire(threadA); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Snapshot(context.Background(), threadA, h, DefaultLimits()); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v", err)
	}
	writer.Close()
	if _, err := h.Snapshot(context.Background(), threadB, h, DefaultLimits()); err == nil {
		t.Fatal("accepted missing thread")
	}
	if err := writer.acquire(threadB); err != nil {
		t.Fatalf("failed inspection leaked a lock: %v", err)
	}
	writer.Close()
}

func TestLegacyCopiedMetadataAndDelayedMetadata(t *testing.T) {
	h := fixtureHome(t)
	data := fixtureBytes(t, threadA, "legacy", nil, "main")
	ancestor := fixtureBytes(t, threadB, "legacy", nil, "copied ancestor")
	data = append(data, ancestor...)
	// Older legacy files can have a record before their own metadata.
	data = append([]byte("{\"type\":\"event_msg\",\"timestamp\":\"2026-10-06T12:34:56Z\",\"payload\":{\"type\":\"shutdown_complete\"}}\n"), data...)
	writeFixture(t, h, fixturePath(threadA, threadA), data)
	if got := inspect(t, h, threadA).Files[0]; got.SHA256 != hash(data) || got.ThreadID != threadA {
		t.Fatal("legacy fork was reinterpreted")
	}
}

func TestLimitsAndCanceledSnapshotDoNotPublishAnything(t *testing.T) {
	h := fixtureHome(t)
	writeFixture(t, h, fixturePath(threadA, threadA), fixtureBytes(t, threadA, "legacy", nil, strings.Repeat("x", 4096)))
	limits := DefaultLimits()
	limits.RecordBytes = 2048
	if _, err := h.Inspect(context.Background(), threadA, h, limits); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("got %v", err)
	}
	limits.RecordBytes = 0
	if _, err := h.Inspect(context.Background(), threadA, h, limits); err == nil {
		t.Fatal("accepted zero limit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.Snapshot(ctx, threadA, h, DefaultLimits()); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(h.Path, writerDirectory)); !os.IsNotExist(err) {
		t.Fatal("canceled snapshot initialized locks")
	}
}

func TestSQLiteDoesNotFallBackFromBrokenAuthoritativeSelection(t *testing.T) {
	for _, kind := range []string{"missing", "outside", "wrong mode", "wrong archive", "other thread"} {
		t.Run(kind, func(t *testing.T) {
			h := fixtureHome(t)
			path := fixturePath(threadA, threadA)
			writeFixture(t, h, path, fixtureBytes(t, threadA, "paginated", nil, "not a fallback"))
			db := fixtureDB(t, h, true)
			selected, mode, archived := path, "paginated", 0
			switch kind {
			case "missing":
				selected = fixturePath(threadA, rolloutA)
			case "outside":
				selected = "../outside.jsonl"
			case "wrong mode":
				mode = "legacy"
			case "wrong archive":
				archived = 1
			case "other thread":
				selected = fixturePath(threadB, threadB)
				writeFixture(t, h, selected, fixtureBytes(t, threadB, "paginated", nil, "other"))
			}
			if _, err := db.Exec("INSERT INTO threads VALUES (?, ?, ?, ?)", threadA, selected, archived, mode); err != nil {
				t.Fatal(err)
			}
			if _, err := h.Inspect(context.Background(), threadA, h, DefaultLimits()); err == nil {
				t.Fatal("accepted broken authoritative selection")
			}
		})
	}
}

func TestLogicalHomePathInSQLite(t *testing.T) {
	h := fixtureHome(t)
	link := filepath.Join(t.TempDir(), "logical")
	if err := os.Symlink(h.Path, link); err != nil {
		t.Fatal(err)
	}
	linked, err := OpenHome(link)
	if err != nil {
		t.Fatal(err)
	}
	defer linked.Close()
	path := fixturePath(threadA, threadA)
	writeFixture(t, h, path, fixtureBytes(t, threadA, "paginated", nil, "linked"))
	db := fixtureDB(t, h, true)
	if _, err := db.Exec("INSERT INTO threads VALUES (?, ?, 0, 'paginated')", threadA, filepath.Join(link, path)); err != nil {
		t.Fatal(err)
	}
	if _, err := linked.Inspect(context.Background(), threadA, linked, DefaultLimits()); err != nil {
		t.Fatal(err)
	}
}

func TestUnknownDatabaseAndStaleOlderDatabase(t *testing.T) {
	for _, kind := range []string{"new schema", "only old", "old alongside current"} {
		t.Run(kind, func(t *testing.T) {
			h := fixtureHome(t)
			path := fixturePath(threadA, threadA)
			writeFixture(t, h, path, fixtureBytes(t, threadA, "legacy", nil, "old"))
			name := "state_3.sqlite"
			if kind == "new schema" {
				name = "state_6.sqlite"
			}
			if err := os.WriteFile(filepath.Join(h.Path, name), []byte("must not read this stale DB"), 0o600); err != nil {
				t.Fatal(err)
			}
			if kind == "old alongside current" {
				fixtureDB(t, h, false)
			}
			_, err := h.Inspect(context.Background(), threadA, h, DefaultLimits())
			if kind == "old alongside current" && err != nil {
				t.Fatal(err)
			}
			if kind != "old alongside current" && err == nil {
				t.Fatal("ignored an unsupported database")
			}
		})
	}
}

func TestResolveHostHome(t *testing.T) {
	for _, tc := range []struct{ explicit, env, home, caller, want string }{
		{"/chosen/.codex", "/env", "/home/user", "/caller", "/chosen/.codex"},
		{"relative with spaces", "/env", "/home/user", "/caller", "/caller/relative with spaces"},
		{"", "relative-env", "/home/user", "/caller", "/caller/relative-env"},
		{"", "", "/home/user", "/caller", "/home/user/.codex"},
	} {
		got, err := ResolveHostHome(tc.explicit, tc.env, tc.home, tc.caller)
		if err != nil || got != tc.want {
			t.Fatalf("got %q %v, want %q", got, err, tc.want)
		}
	}
}
