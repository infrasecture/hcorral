package session

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
