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
