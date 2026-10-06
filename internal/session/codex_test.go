package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/infrasecture/hcorral/internal/sessionconfig"
)

// Opt-in native format qualification. An isolated app server resumes synthetic
// histories and sends a follow-up to a loopback-only mock provider. No account
// or external model call is needed. HOME, CODEX_HOME and workspaces are disposable.
func TestCodexResumesNativeHistory(t *testing.T) {
	binary := os.Getenv("HCORRAL_TEST_CODEX")
	if binary == "" {
		t.Skip("set HCORRAL_TEST_CODEX to an explicitly selected Codex executable")
	}
	peerBinary := os.Getenv("HCORRAL_TEST_CODEX_PEER")
	if peerBinary == "" {
		peerBinary = binary
	}
	for _, mode := range []string{"legacy", "paginated", "paginated-prefix", "revert-prefix", "archived-revert-prefix"} {
		for _, existingHome := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/existing=%v", mode, existingHome), func(t *testing.T) {
				source, h := fixtureHome(t), fixtureHome(t)
				workspace := t.TempDir()
				requests := make(chan string, 4)
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost {
						http.Error(w, "no such fixture endpoint", http.StatusNotFound)
						return
					}
					body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
					if err != nil {
						http.Error(w, err.Error(), http.StatusBadRequest)
						return
					}
					select {
					case requests <- string(body):
					default:
					}
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_fixture\"}}\n\n")
					fmt.Fprint(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"id\":\"msg_fixture\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"fixture reply\"}]}}\n\n")
					fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_fixture\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
				}))
				t.Cleanup(provider.Close)
				id := threadA
				if strings.HasSuffix(mode, "prefix") {
					prefix := nativeFixture(t, threadA, "paginated", nil, "inherited message")
					id = threadB
					parentRollout, childRollout := rolloutA, threadB
					if mode != "paginated-prefix" {
						id, parentRollout, childRollout = threadA, threadA, rolloutA
					}
					parentPath := fixturePath(threadA, parentRollout) + ".zst"
					var tail bytes.Buffer
					if err := json.NewEncoder(&tail).Encode(map[string]any{"timestamp": "2026-10-06T12:34:57Z", "ordinal": 3, "type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "private parent continuation"}}}}); err != nil {
						t.Fatal(err)
					}
					writeFixture(t, source, parentPath, append(append([]byte(nil), prefix...), tail.Bytes()...))
					base := &HistoryPosition{RolloutID: parentRollout, EndOrdinalExclusive: 3, EndByteOffset: uint64(len(prefix))}
					childPath := fixturePath(id, childRollout)
					if mode == "archived-revert-prefix" {
						childPath = "archived_sessions/" + filepath.Base(childPath)
					}
					writeFixture(t, source, childPath, nativeFixture(t, id, "paginated", base, "child message"))
					if mode != "paginated-prefix" {
						db := fixtureDB(t, source, true)
						if _, err := db.Exec("INSERT INTO threads VALUES (?, ?, ?, 'paginated')", id, filepath.Join(source.Path, childPath), mode == "archived-revert-prefix"); err != nil {
							t.Fatal(err)
						}
					}
				} else {
					writeFixture(t, source, fixturePath(id, id), nativeFixture(t, id, mode, nil, "persisted message"))
				}
				config := fmt.Sprintf("model = \"fixture-model\"\nmodel_provider = \"test-provider\"\n[model_providers.test-provider]\nname = \"Local fixture\"\nbase_url = %q\nwire_api = \"responses\"\nrequires_openai_auth = false\n", provider.URL+"/v1")
				if err := os.WriteFile(filepath.Join(h.Path, "config.toml"), []byte(config), 0o600); err != nil {
					t.Fatal(err)
				}
				var server *codexServer
				var priorSelection *selection
				if existingHome {
					writeFixture(t, h, fixturePath(threadC, threadC), nativeFixture(t, threadC, "paginated", nil, "unrelated existing conversation"))
					server = startCodex(t, binary, h.Path, workspace)
					server.call(t, "thread/list", map[string]any{"limit": 100, "modelProviders": []string{"test-provider"}})
					var err error
					priorSelection, err = h.selection(context.Background(), threadC)
					if err != nil || priorSelection == nil {
						t.Fatalf("existing home did not initialize its metadata: %+v %v", priorSelection, err)
					}
				}
				// Exercise the actual format/stream/staging/publication pipeline. The
				// existing-home case keeps Codex alive after initial database backfill.
				imported := published(t, received(t, h, exported(t, source, id)))
				if imported.Archived != (mode == "archived-revert-prefix") {
					t.Fatal("transfer changed archive state")
				}
				if server == nil {
					server = startCodex(t, binary, h.Path, workspace)
				}
				listed := server.call(t, "thread/list", map[string]any{"archived": mode == "archived-revert-prefix", "limit": 100, "modelProviders": []string{"test-provider"}})
				var listing struct {
					Data []struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				if err := json.Unmarshal(listed, &listing); err != nil {
					t.Fatal(err)
				}
				found := false
				for _, thread := range listing.Data {
					if thread.ID == id {
						found = true
					}
					if mode == "paginated-prefix" && thread.ID == threadA {
						t.Fatal("prerequisite appeared as an active conversation")
					}
				}
				if !found {
					t.Fatalf("requested thread is missing from native picker: %s", listed)
				}
				if mode == "paginated-prefix" {
					archived := server.call(t, "thread/list", map[string]any{"archived": true, "limit": 100, "modelProviders": []string{"test-provider"}})
					if err := json.Unmarshal(archived, &listing); err != nil {
						t.Fatal(err)
					}
					foundParent := false
					for _, thread := range listing.Data {
						if thread.ID == threadA {
							foundParent = true
						}
					}
					parentSelection, err := h.selection(context.Background(), threadA)
					if err != nil {
						t.Fatal(err)
					}
					t.Logf("prerequisite archived picker visibility=%v; SQLite selection=%+v", foundParent, parentSelection)
				}
				selected, err := h.selection(context.Background(), id)
				if err != nil || (selected != nil && strings.Contains(selected.path, ".hcorral-history")) {
					t.Fatalf("native backfill selected a prerequisite: %+v %v", selected, err)
				}
				if existingHome {
					after, err := h.selection(context.Background(), threadC)
					if err != nil || after == nil || *after != *priorSelection {
						t.Fatalf("transfer changed unrelated selection: %+v %v", after, err)
					}
				}
				if mode == "archived-revert-prefix" {
					// Preserve archive state on import; resuming requires a separate,
					// explicit user action. Exercise that action only in this fixture.
					server.call(t, "thread/unarchive", map[string]string{"threadId": id})
				}
				result := server.call(t, "thread/resume", map[string]any{"threadId": id, "cwd": workspace, "modelProvider": "test-provider", "approvalPolicy": "never", "sandbox": "read-only"})
				var resumed struct {
					Thread struct {
						ID string `json:"id"`
					} `json:"thread"`
				}
				if err := json.Unmarshal(result, &resumed); err != nil || resumed.Thread.ID != id {
					t.Fatalf("resume returned another thread: %s (%v)", result, err)
				}
				// Resuming really owns the thread; copying it concurrently must fail.
				if _, err := h.Snapshot(context.Background(), id, h, DefaultLimits()); !errors.Is(err, ErrBusy) {
					t.Fatalf("native Codex resume did not exclude snapshot writer acquisition: %v", err)
				}
				server.call(t, "turn/start", map[string]any{"threadId": id, "input": []any{map[string]string{"type": "text", "text": "fixture follow-up"}}})
				select {
				case body := <-requests:
					if strings.Contains(body, "private parent continuation") || strings.Contains(body, "unrelated existing conversation") {
						t.Fatal("transfer leaked unrelated history into resumed context")
					}
					wanted := []string{"persisted message", "fixture follow-up"}
					if strings.HasSuffix(mode, "prefix") {
						wanted = []string{"inherited message", "child message", "fixture follow-up"}
					}
					for _, text := range wanted {
						if !strings.Contains(body, text) {
							t.Fatalf("resumed model context lacks %q: %.2000s", text, body)
						}
					}
				case <-time.After(10 * time.Second):
					t.Fatal("Codex did not send resumed context to the loopback provider")
				}
				server.waitNotification(t, "turn/completed")
				server.finish(t)
				// Re-export bytes actually persisted by native Codex, including
				// its new turn context and any legacy-to-paginated migration.
				// Qualify the reverse direction with a separately selected peer.
				peerHome := fixtureHome(t)
				if err := os.WriteFile(filepath.Join(peerHome.Path, "config.toml"), []byte(config), 0o600); err != nil {
					t.Fatal(err)
				}
				published(t, received(t, peerHome, exported(t, h, id)))
				peer := startCodex(t, peerBinary, peerHome.Path, workspace)
				peer.call(t, "thread/resume", map[string]any{"threadId": id, "cwd": workspace, "modelProvider": "test-provider", "approvalPolicy": "never", "sandbox": "read-only"})
				peer.call(t, "turn/start", map[string]any{"threadId": id, "input": []any{map[string]string{"type": "text", "text": "peer follow-up"}}})
				select {
				case body := <-requests:
					for _, text := range []string{"fixture follow-up", "fixture reply", "peer follow-up"} {
						if !strings.Contains(body, text) {
							t.Fatalf("native-written transfer lost %q: %.2000s", text, body)
						}
					}
					if strings.Contains(body, "private parent continuation") || strings.Contains(body, "unrelated existing conversation") {
						t.Fatal("native-written transfer leaked unrelated history")
					}
				case <-time.After(10 * time.Second):
					t.Fatal("peer did not resume native-written history")
				}
				t.Logf("Codex resumed %s as %s", mode, id)
			})
		}
	}
}

func nativeFixture(t *testing.T, id, mode string, base *HistoryPosition, text string) []byte {
	t.Helper()
	data := fixtureBytes(t, id, mode, base, text)
	event := map[string]any{"timestamp": "2026-10-06T12:34:56Z", "type": "event_msg", "payload": map[string]any{"type": "user_message", "message": text}}
	if mode == "paginated" {
		ordinal := uint64(2)
		if base != nil {
			ordinal += base.EndOrdinalExclusive
		}
		event["ordinal"] = ordinal
		event["payload"] = map[string]any{"type": "item_completed", "thread_id": id, "turn_id": "fixture-turn", "item": map[string]any{"type": "UserMessage", "id": "fixture-user", "content": []any{map[string]any{"type": "text", "text": text, "text_elements": []any{}}}}}
	}
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(event); err != nil {
		t.Fatal(err)
	}
	return append(data, b.Bytes()...)
}

type codexServer struct {
	stdin         io.WriteCloser
	scanner       *bufio.Scanner
	sequence      int
	notifications []string
	cmd           *exec.Cmd
	cancel        context.CancelFunc
	finished      bool
}

func startCodex(t *testing.T, binary, home, workspace string) *codexServer {
	return startCodexWithEnv(t, binary, home, workspace, nil)
}

func startCodexWithEnv(t *testing.T, binary, home, workspace string, extraEnv []string) *codexServer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cmd := exec.CommandContext(ctx, binary, "app-server", "--listen", "stdio://")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "CODEX_HOME=" + home, "RUST_LOG=error"}
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Dir = workspace
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	s := &codexServer{stdin: stdin, scanner: bufio.NewScanner(stdout), cmd: cmd, cancel: cancel}
	t.Cleanup(func() {
		if !s.finished {
			stdin.Close()
			cancel()
			cmd.Wait()
		}
		if t.Failed() {
			t.Logf("isolated Codex stderr: %s", stderr.String())
		}
	})
	s.scanner.Buffer(make([]byte, 64<<10), 16<<20)
	initialized := s.call(t, "initialize", map[string]any{"clientInfo": map[string]string{"name": "hcorral_test", "version": "0.1"}, "capabilities": map[string]bool{"experimentalApi": true}})
	var initialization struct {
		UserAgent string `json:"userAgent"`
	}
	if err := json.Unmarshal(initialized, &initialization); err != nil {
		t.Fatal(err)
	}
	t.Logf("native runtime: %s", initialization.UserAgent)
	if err := json.NewEncoder(stdin).Encode(map[string]any{"method": "initialized"}); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNativeCodexSQLiteLocationMatchesDiscovery(t *testing.T) {
	binary := os.Getenv("HCORRAL_TEST_CODEX")
	if binary == "" {
		t.Skip("set HCORRAL_TEST_CODEX to a selected Codex executable")
	}
	for _, name := range []string{"/etc/codex/config.toml", "/etc/codex/requirements.toml", "/etc/codex/managed_config.toml"} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Skip("native SQLite qualification requires an unmanaged system configuration")
		}
	}
	for _, mode := range []string{"default", "environment relative", "user config over environment"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			home := filepath.Join(root, "home")
			workspace := filepath.Join(root, "workspace")
			for _, dir := range []string{home, workspace} {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			config := "model='fixture-model'\nmodel_provider='test-provider'\n[model_providers.test-provider]\nname='Local fixture'\nbase_url='http://127.0.0.1:1/v1'\nwire_api='responses'\nrequires_openai_auth=false\n"
			environment := ""
			if mode != "default" {
				environment = " relative state \n"
			}
			if mode == "user config over environment" {
				config = "sqlite_home='../user state'\n" + config
			}
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			resolved, err := sessionconfig.SQLiteHome(context.Background(), sessionconfig.SQLiteOptions{Home: home, CWD: workspace, Environment: environment}, sessionconfig.ReadLocalFile)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(resolved.Path, root+string(filepath.Separator)) {
				t.Fatalf("fixture resolved outside its disposable root: %q", resolved.Path)
			}
			server := startCodexWithEnv(t, binary, home, workspace, []string{"CODEX_SQLITE_HOME=" + environment})
			server.call(t, "thread/list", map[string]any{"limit": 1})
			server.finish(t)
			if _, err := os.Stat(filepath.Join(resolved.Path, "state_5.sqlite")); err != nil {
				t.Fatalf("native Codex did not use the resolved SQLite home %q: %v", resolved.Path, err)
			}
			if resolved.Path != home {
				if _, err := os.Stat(filepath.Join(home, "state_5.sqlite")); !os.IsNotExist(err) {
					t.Fatal("native Codex also initialized the default database")
				}
			}
		})
	}
}

func (s *codexServer) call(t *testing.T, method string, params any) json.RawMessage {
	t.Helper()
	s.sequence++
	if err := json.NewEncoder(s.stdin).Encode(map[string]any{"id": s.sequence, "method": method, "params": params}); err != nil {
		t.Fatal(err)
	}
	for s.scanner.Scan() {
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(s.scanner.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if string(envelope.ID) != fmt.Sprint(s.sequence) {
			if envelope.Method != "" {
				s.notifications = append(s.notifications, envelope.Method)
			}
			continue
		}
		if len(envelope.Error) != 0 {
			t.Fatalf("%s: %s", method, envelope.Error)
		}
		return envelope.Result
	}
	t.Fatalf("Codex exited while handling %s: %v", method, s.scanner.Err())
	return nil
}

func (s *codexServer) waitNotification(t *testing.T, method string) {
	t.Helper()
	for _, pending := range s.notifications {
		if pending == method {
			return
		}
	}
	for s.scanner.Scan() {
		var notification struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(s.scanner.Bytes(), &notification); err != nil {
			t.Fatal(err)
		}
		if notification.Method == method {
			return
		}
	}
	t.Fatalf("Codex exited before %s: %v", method, s.scanner.Err())
}

func (s *codexServer) finish(t *testing.T) {
	t.Helper()
	s.stdin.Close()
	err := s.cmd.Wait()
	s.cancel()
	s.finished = true
	if err != nil {
		t.Fatalf("Codex did not shut down cleanly after completing its turn: %v", err)
	}
}
