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
)

// Opt-in native format qualification. An isolated app server resumes synthetic
// histories and sends a follow-up to a loopback-only mock provider. No account
// or external model call is needed. HOME, CODEX_HOME and workspaces are disposable.
func TestCodexResumesNativeHistory(t *testing.T) {
	binary := os.Getenv("HCORRAL_TEST_CODEX")
	if binary == "" {
		t.Skip("set HCORRAL_TEST_CODEX to an explicitly selected Codex executable")
	}
	for _, mode := range []string{"legacy", "paginated", "paginated-prefix", "revert-prefix", "archived-revert-prefix"} {
		t.Run(mode, func(t *testing.T) {
			h := fixtureHome(t)
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
				parentPath := "archived_sessions/.hcorral-history/" + parentRollout + "/" + filepath.Base(fixturePath(threadA, parentRollout))
				writeFixture(t, h, parentPath, prefix)
				base := &HistoryPosition{RolloutID: parentRollout, EndOrdinalExclusive: 3, EndByteOffset: uint64(len(prefix))}
				childPath := fixturePath(id, childRollout)
				if mode == "archived-revert-prefix" {
					childPath = "archived_sessions/" + filepath.Base(childPath)
				}
				writeFixture(t, h, childPath, nativeFixture(t, id, "paginated", base, "child message"))
			} else {
				writeFixture(t, h, fixturePath(id, id), nativeFixture(t, id, mode, nil, "persisted message"))
			}
			config := fmt.Sprintf("model = \"fixture-model\"\nmodel_provider = \"test-provider\"\n[model_providers.test-provider]\nname = \"Local fixture\"\nbase_url = %q\nwire_api = \"responses\"\nrequires_openai_auth = false\n", provider.URL+"/v1")
			if err := os.WriteFile(filepath.Join(h.Path, "config.toml"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			server := startCodex(t, binary, h.Path, workspace)
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
			if err != nil || selected == nil || strings.Contains(selected.path, ".hcorral-history") {
				t.Fatalf("native backfill selected a prerequisite: %+v %v", selected, err)
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
			t.Logf("Codex resumed %s as %s", mode, id)
		})
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
	stdin    io.WriteCloser
	scanner  *bufio.Scanner
	sequence int
}

func startCodex(t *testing.T, binary, home, workspace string) *codexServer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	cmd := exec.CommandContext(ctx, binary, "app-server", "--listen", "stdio://")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "CODEX_HOME=" + home, "RUST_LOG=error"}
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
	t.Cleanup(func() {
		stdin.Close()
		cancel()
		cmd.Wait()
		if t.Failed() {
			t.Logf("isolated Codex stderr: %s", stderr.String())
		}
	})
	s := &codexServer{stdin: stdin, scanner: bufio.NewScanner(stdout)}
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
		}
		if err := json.Unmarshal(s.scanner.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if string(envelope.ID) != fmt.Sprint(s.sequence) {
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
