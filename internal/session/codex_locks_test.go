package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A transfer guard must exclude native operations which move history or change
// its selected path, as well as ordinary resumed writers. Each refused request
// is repeated successfully after release, proving the refusal was the guard,
// not an invalid fixture or unsupported app-server method.
func TestNativeCodexLifecycleRespectsTransferLocks(t *testing.T) {
	binary := os.Getenv("HCORRAL_TEST_CODEX")
	if binary == "" {
		t.Skip("set HCORRAL_TEST_CODEX to a selected Codex executable")
	}
	for _, mode := range []string{"legacy", "paginated"} {
		for _, operation := range []string{"archive", "unarchive", "resume", "delete"} {
			t.Run(mode+"/"+operation, func(t *testing.T) {
				home := fixtureHome(t)
				workspace := t.TempDir()
				name := fixturePath(threadA, threadA)
				archived := operation == "unarchive"
				if archived {
					name = "archived_sessions/" + filepath.Base(name)
				}
				writeFixture(t, home, name, nativeFixture(t, threadA, mode, nil, "history protected by transfer lock"))
				config := "model='fixture-model'\nmodel_provider='test-provider'\n[model_providers.test-provider]\nname='Offline fixture'\nbase_url='http://127.0.0.1:9/v1'\nwire_api='responses'\nrequires_openai_auth=false\n"
				writeFixture(t, home, "config.toml", []byte(config))
				server := startCodex(t, binary, home.Path, workspace)
				server.call(t, "thread/list", map[string]any{"archived": archived, "limit": 100, "modelProviders": []string{"test-provider"}})
				guard, err := home.Snapshot(context.Background(), threadA, home, DefaultLimits())
				if err != nil {
					t.Fatal(err)
				}
				defer guard.Close()
				before, err := home.selection(context.Background(), threadA)
				if err != nil || before == nil || before.archived != archived {
					t.Fatalf("native index did not select the fixture: %+v %v", before, err)
				}
				original, err := os.ReadFile(before.path)
				if err != nil {
					t.Fatal(err)
				}
				params := map[string]any{"threadId": threadA}
				if operation == "resume" {
					params["cwd"], params["modelProvider"] = workspace, "test-provider"
					params["approvalPolicy"], params["sandbox"] = "never", "read-only"
				}
				_, rpcError := server.request(t, "thread/"+operation, params)
				if len(rpcError) == 0 || !strings.Contains(string(rpcError), "writer") {
					t.Fatalf("native %s did not refuse the transfer's writer guard: %s", operation, rpcError)
				}
				after, err := home.selection(context.Background(), threadA)
				if err != nil || after == nil || *after != *before {
					t.Fatalf("refused %s changed selection: %+v -> %+v (%v)", operation, before, after, err)
				}
				current, err := os.ReadFile(before.path)
				if err != nil || !bytes.Equal(current, original) {
					t.Fatalf("refused %s changed persisted history: %v", operation, err)
				}
				if err := guard.Close(); err != nil {
					t.Fatal(err)
				}
				server.call(t, "thread/"+operation, params)
				server.finish(t)
				after, err = home.selection(context.Background(), threadA)
				if operation == "delete" {
					if err != nil || after != nil {
						t.Fatalf("released delete retained the selected thread: %+v %v", after, err)
					}
					if _, err := os.Stat(before.path); !os.IsNotExist(err) {
						t.Fatalf("released delete retained the fixture rollout: %v", err)
					}
					return
				}
				if err != nil || after == nil || after.archived != (operation == "archive") {
					t.Fatalf("released %s did not complete: %+v %v", operation, after, err)
				}
			})
		}
	}
}

// Revert changes the authoritative rollout while preserving the stable thread
// ID. Exercise the public native operation on actual completed turns, then
// transfer its selected replacement without the removed private continuation.
func TestNativeCodexRevertAndTransferPreserveSelectedPrefix(t *testing.T) {
	binary := os.Getenv("HCORRAL_TEST_CODEX")
	if binary == "" {
		t.Skip("set HCORRAL_TEST_CODEX to a selected Codex executable")
	}
	home := fixtureHome(t)
	workspace := t.TempDir()
	writeFixture(t, home, fixturePath(threadA, threadA), nativeFixture(t, threadA, "paginated", nil, "original retained history"))
	requests := promotionProvider(t, home)
	server := startCodex(t, binary, home.Path, workspace)
	server.call(t, "thread/resume", map[string]any{"threadId": threadA, "cwd": workspace, "modelProvider": "test-provider", "approvalPolicy": "never", "sandbox": "read-only"})
	var secondTurn string
	for _, prompt := range []string{"retained completed turn", "private reverted continuation"} {
		server.notifications = nil
		result := server.call(t, "turn/start", map[string]any{"threadId": threadA, "input": []any{map[string]string{"type": "text", "text": prompt}}})
		var started struct {
			Turn struct{ ID string } `json:"turn"`
		}
		if err := json.Unmarshal(result, &started); err != nil || started.Turn.ID == "" {
			t.Fatalf("native turn has no ID: %s (%v)", result, err)
		}
		secondTurn = started.Turn.ID
		select {
		case body := <-requests:
			if !strings.Contains(body, prompt) {
				t.Fatalf("native turn did not send its prompt: %.2000s", body)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("native turn did not reach the local provider")
		}
		server.waitNotification(t, "turn/completed")
	}
	before, err := home.selection(context.Background(), threadA)
	if err != nil || before == nil {
		t.Fatalf("native history has no authoritative selection: %+v %v", before, err)
	}
	if guard, err := home.Snapshot(context.Background(), threadA, home, DefaultLimits()); !errors.Is(err, ErrBusy) {
		if guard != nil {
			guard.Close()
		}
		t.Fatalf("native writer allowed a transfer before revert: %v", err)
	}
	params := map[string]any{"threadId": threadA, "beforeTurnId": secondTurn}
	server.call(t, "thread/revert", params)
	if guard, err := home.Snapshot(context.Background(), threadA, home, DefaultLimits()); !errors.Is(err, ErrBusy) {
		if guard != nil {
			guard.Close()
		}
		t.Fatalf("native writer allowed a transfer after revert: %v", err)
	}
	server.finish(t)
	after, err := home.selection(context.Background(), threadA)
	if err != nil || after == nil || after.path == before.path {
		t.Fatalf("native revert did not select a replacement rollout: %+v %v", after, err)
	}

	// Transfer the native-created replacement, then verify the actual provider
	// context. The old full rollout still exists, but its excluded tail must not
	// enter either the transferred bytes or the resumed conversation.
	destination := fixtureHome(t)
	stream := exported(t, home, threadA)
	if bytes.Contains(stream, []byte("private reverted continuation")) {
		t.Fatal("export included the reverted continuation")
	}
	published(t, received(t, destination, stream))
	requests = promotionProvider(t, destination)
	resumed := startCodex(t, binary, destination.Path, workspace)
	resumed.call(t, "thread/resume", map[string]any{"threadId": threadA, "cwd": workspace, "modelProvider": "test-provider", "approvalPolicy": "never", "sandbox": "read-only"})
	resumed.call(t, "turn/start", map[string]any{"threadId": threadA, "input": []any{map[string]string{"type": "text", "text": "verify reverted transfer"}}})
	select {
	case body := <-requests:
		if !strings.Contains(body, "original retained history") || !strings.Contains(body, "retained completed turn") || strings.Contains(body, "private reverted continuation") {
			t.Fatalf("transferred native revert selected the wrong context: %.2000s", body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("native Codex did not resume transferred reverted history")
	}
	resumed.waitNotification(t, "turn/completed")
	resumed.finish(t)
}

// Legacy Git metadata appends session_meta to the rollout; paginated metadata
// lives only in SQLite. Qualify both paths rather than assuming that every
// successful metadata update necessarily modified the guarded conversation.
func TestNativeCodexMetadataRespectsTransferHistory(t *testing.T) {
	binary := os.Getenv("HCORRAL_TEST_CODEX")
	if binary == "" {
		t.Skip("set HCORRAL_TEST_CODEX to a selected Codex executable")
	}
	for _, mode := range []string{"legacy", "paginated"} {
		t.Run(mode, func(t *testing.T) {
			home := fixtureHome(t)
			name := fixturePath(threadA, threadA)
			original := nativeFixture(t, threadA, mode, nil, "metadata fixture history")
			writeFixture(t, home, name, original)
			promotionProvider(t, home)
			server := startCodex(t, binary, home.Path, t.TempDir())
			server.call(t, "thread/list", map[string]any{"limit": 100, "modelProviders": []string{"test-provider"}})
			before, err := home.selection(context.Background(), threadA)
			if err != nil || before == nil || before.mode != mode {
				t.Fatalf("native index did not preserve the fixture's history mode: %+v %v", before, err)
			}
			guard, err := home.Snapshot(context.Background(), threadA, home, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Close()
			params := map[string]any{"threadId": threadA, "gitInfo": map[string]string{"branch": "native-fixture-branch"}}
			_, rpcError := server.request(t, "thread/metadata/update", params)
			if mode == "legacy" {
				if len(rpcError) == 0 || !strings.Contains(string(rpcError), "writer") {
					t.Fatalf("legacy metadata mutation did not refuse the transfer guard: %s", rpcError)
				}
			} else if len(rpcError) != 0 {
				t.Fatalf("paginated database-only metadata update failed: %s", rpcError)
			}
			current, err := os.ReadFile(before.path)
			if err != nil || !bytes.Equal(current, original) {
				t.Fatalf("metadata operation modified guarded history: %v", err)
			}
			after, err := home.selection(context.Background(), threadA)
			if err != nil || after == nil || *after != *before {
				t.Fatalf("metadata operation changed selected history: %+v -> %+v (%v)", before, after, err)
			}
			state, err := home.openStateDB(false)
			if err != nil || state == nil {
				t.Fatalf("native fixture database disappeared: %v", err)
			}
			defer state.close()
			var branch string
			if err := state.db.QueryRow("SELECT coalesce(git_branch, '') FROM threads WHERE id = ?", threadA).Scan(&branch); err != nil {
				t.Fatal(err)
			}
			if (branch == "native-fixture-branch") != (mode == "paginated") {
				t.Fatalf("guarded %s metadata update left unexpected database value: %q", mode, branch)
			}
			if err := guard.Close(); err != nil {
				t.Fatal(err)
			}
			server.call(t, "thread/metadata/update", params)
			server.finish(t)
			if err := state.db.QueryRow("SELECT coalesce(git_branch, '') FROM threads WHERE id = ?", threadA).Scan(&branch); err != nil || branch != "native-fixture-branch" {
				t.Fatalf("released metadata update did not persist: %q %v", branch, err)
			}
			current, err = os.ReadFile(before.path)
			if err != nil || bytes.Equal(current, original) != (mode == "paginated") {
				t.Fatalf("released %s metadata update used the wrong storage: %v", mode, err)
			}
			result := published(t, received(t, fixtureHome(t), exported(t, home, threadA)))
			if result.ThreadID != threadA {
				t.Fatal("metadata update changed the transferred thread identity")
			}
		})
	}
}

func TestNativeCodexCompressionRespectsTransferLocks(t *testing.T) {
	binary := os.Getenv("HCORRAL_TEST_CODEX")
	if binary == "" {
		t.Skip("set HCORRAL_TEST_CODEX to a selected Codex executable")
	}
	home := fixtureHome(t)
	path := fixturePath(threadA, threadA)
	original := nativeFixture(t, threadA, "paginated", nil, "cold guarded history")
	writeFixture(t, home, path, original)
	other := fixturePath(threadB, threadB)
	writeFixture(t, home, other, nativeFixture(t, threadB, "paginated", nil, "unrelated cold history"))
	cold := time.Now().Add(-8 * 24 * time.Hour)
	for _, name := range []string{path, other} {
		if err := os.Chtimes(filepath.Join(home.Path, name), cold, cold); err != nil {
			t.Fatal(err)
		}
	}
	guard, err := home.Snapshot(context.Background(), threadA, home, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	promotionProvider(t, home)
	server := startCodex(t, binary, home.Path, t.TempDir())
	server.call(t, "rollout/compress", map[string]any{})
	waitCompression := func(name string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			_, fileErr := os.Stat(filepath.Join(home.Path, name+".zst"))
			_, markerErr := os.Stat(filepath.Join(home.Path, ".tmp/rollout-compression.lock"))
			lock, lockErr := os.OpenFile(filepath.Join(home.Path, ".tmp/rollout-maintenance.lock"), os.O_RDWR, 0)
			if lockErr == nil {
				lockErr = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
				lock.Close()
			}
			if fileErr == nil && markerErr == nil && lockErr == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("native compression did not complete for %s", name)
	}
	// Successful compression of the unrelated file plus the released native
	// maintenance lock prove that the worker ran and completed its publication.
	waitCompression(other)
	current, err := os.ReadFile(filepath.Join(home.Path, path))
	if err != nil || !bytes.Equal(current, original) {
		t.Fatalf("native compression changed guarded history: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home.Path, path+".zst")); !os.IsNotExist(err) {
		t.Fatalf("native compression published while the transfer guard was held: %v", err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	// The completed worker leaves a rate-limit marker. Removing only this
	// disposable home's marker allows an immediate second actual worker pass.
	if err := os.Remove(filepath.Join(home.Path, ".tmp/rollout-compression.lock")); err != nil {
		t.Fatal(err)
	}
	server.call(t, "rollout/compress", map[string]any{})
	waitCompression(path)
	server.finish(t)
	if _, err := os.Stat(filepath.Join(home.Path, path)); !os.IsNotExist(err) {
		t.Fatalf("released compression retained the original representation: %v", err)
	}
	plan := inspect(t, home, threadA)
	if len(plan.Files) != 1 || plan.Files[0].SHA256 != hash(original) {
		t.Fatal("native compression changed the decoded transfer history")
	}
	published(t, received(t, fixtureHome(t), exported(t, home, threadA)))
}

func TestNativeCodexMigrationRespectsTransferLocks(t *testing.T) {
	binary := os.Getenv("HCORRAL_TEST_CODEX")
	if binary == "" {
		t.Skip("set HCORRAL_TEST_CODEX to a selected Codex executable")
	}
	home := fixtureHome(t)
	path := fixturePath(threadA, threadA)
	original := nativeFixture(t, threadA, "legacy", nil, "legacy migration history")
	writeFixture(t, home, path, original)
	writeFixture(t, home, fixturePath(threadB, threadB), nativeFixture(t, threadB, "legacy", nil, "unrelated migration history"))
	promotionProvider(t, home)
	config, err := os.ReadFile(filepath.Join(home.Path, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, home, "config.toml", append(config, []byte("\n[features]\nbackground_paginated_rollout_migration=true\nlocal_thread_store_compression=false\n")...))
	guard, err := home.Snapshot(context.Background(), threadA, home, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	workspace := t.TempDir()
	server := startCodex(t, binary, home.Path, workspace)
	server.call(t, "thread/list", map[string]any{"limit": 100})
	state, err := home.openStateDB(false)
	if err != nil || state == nil {
		t.Fatalf("native migration did not initialize its database: %v", err)
	}
	defer state.close()
	waitMode := func(id string, requireBusy bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		var mode string
		var busy int
		var modeErr, busyErr error
		for time.Now().Before(deadline) {
			modeErr = state.db.QueryRow("SELECT history_mode FROM threads WHERE id = ?", id).Scan(&mode)
			busyErr = state.db.QueryRow("SELECT count(*) FROM rollout_migration_skipped_rollouts WHERE rollout_path = ? AND skip_reason = 'busy'", path).Scan(&busy)
			if modeErr == nil && mode == "paginated" && busyErr == nil && (busy > 0) == requireBusy {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("native migration did not reach paginated history for %s (want busy=%v): mode=%q (%v), busy=%d (%v)", id, requireBusy, mode, modeErr, busy, busyErr)
	}
	// The unguarded thread must migrate, and the guarded thread must be
	// explicitly recorded as busy. A worker that never ran cannot pass.
	waitMode(threadB, true)
	server.finish(t)
	current, err := os.ReadFile(filepath.Join(home.Path, path))
	if err != nil || !bytes.Equal(current, original) {
		t.Fatalf("native migration changed guarded history: %v", err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	server = startCodex(t, binary, home.Path, workspace)
	server.call(t, "thread/list", map[string]any{"limit": 100})
	waitMode(threadA, false)
	server.finish(t)
	plan := inspect(t, home, threadA)
	if len(plan.Files) != 1 || plan.Files[0].Metadata.HistoryMode != "paginated" {
		t.Fatal("released migration did not publish valid paginated history")
	}
	stream := exported(t, home, threadA)
	if !bytes.Contains(stream, []byte("legacy migration history")) || bytes.Contains(stream, []byte("unrelated migration history")) {
		t.Fatal("native migration changed the transferred conversation scope")
	}
	published(t, received(t, fixtureHome(t), stream))
}
