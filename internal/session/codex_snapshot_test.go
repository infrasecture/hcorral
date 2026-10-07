package session

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Hold the real model turn open at a loopback provider while copying. This
// distinguishes an actively running conversation from a merely loaded thread.
// Neither endpoint uses credentials or an external model service.
func TestNativeCodexCopiesDuringRunningTurn(t *testing.T) {
	binary := os.Getenv("HCORRAL_TEST_CODEX")
	if binary == "" {
		t.Skip("set HCORRAL_TEST_CODEX to a selected Codex executable")
	}
	peerBinary := os.Getenv("HCORRAL_TEST_CODEX_PEER")
	if peerBinary == "" {
		peerBinary = binary
	}
	for _, mode := range []string{"legacy", "paginated", "inherited"} {
		t.Run(mode, func(t *testing.T) {
			source, destination := nativeEndpointHome(t), nativeEndpointHome(t)
			workspace := t.TempDir()
			id := threadA
			if mode == "inherited" {
				prefix := nativeFixture(t, threadA, "paginated", nil, "inherited live history")
				writeFixture(t, source, fixturePath(threadA, threadA), prefix)
				id = threadB
				base := &HistoryPosition{RolloutID: threadA, EndOrdinalExclusive: 3, EndByteOffset: uint64(len(prefix))}
				writeFixture(t, source, fixturePath(id, id), nativeFixture(t, id, "paginated", base, "saved child history"))
			} else {
				writeFixture(t, source, fixturePath(id, id), nativeFixture(t, id, mode, nil, "saved live history"))
			}
			requests := make(chan string, 1)
			release := make(chan struct{})
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				requests <- string(body)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_live\"}}\n\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				fmt.Fprint(w, "event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"id\":\"msg_live\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"reply after snapshot\"}]}}\n\n")
				fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_live\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
			}))
			t.Cleanup(provider.Close)
			config := fmt.Sprintf("model='fixture-model'\nmodel_provider='test-provider'\n[model_providers.test-provider]\nname='Local fixture'\nbase_url=%q\nwire_api='responses'\nrequires_openai_auth=false\n", provider.URL+"/v1")
			writeFixture(t, source, "config.toml", []byte(config))
			server := startCodex(t, binary, source.Path, workspace)
			// Cancel/finish cleanup runs before httptest.Close even on failure.
			server.call(t, "thread/resume", map[string]any{"threadId": id, "cwd": workspace, "modelProvider": "test-provider", "approvalPolicy": "never", "sandbox": "read-only"})
			server.call(t, "turn/start", map[string]any{"threadId": id, "input": []any{map[string]string{"type": "text", "text": "prompt during running turn"}}})
			select {
			case <-requests:
			case <-time.After(10 * time.Second):
				t.Fatal("native turn did not reach the held provider")
			}
			// This call must finish before release; a close-first or wait-until-
			// idle implementation cannot pass. Exercise the public CLI as well
			// when the Docker qualification harness is enabled.
			result := nativeEndpointTransfer(t, source, destination, id, false)
			data, err := os.ReadFile(filepath.Join(destination.Path, result.MainPath))
			if err != nil || !bytes.Contains(data, []byte("prompt during running turn")) || bytes.Contains(data, []byte("reply after snapshot")) {
				t.Fatalf("snapshot missed saved prompt or included a future reply: %v", err)
			}
			close(release)
			server.waitNotification(t, "turn/completed")
			later := exported(t, source, id)
			if !bytes.Contains(later, []byte("reply after snapshot")) {
				t.Fatal("original conversation did not continue after copying")
			}
			peerRequests := promotionProvider(t, destination)
			peer := startCodex(t, peerBinary, destination.Path, workspace)
			peer.call(t, "thread/resume", map[string]any{"threadId": id, "cwd": workspace, "modelProvider": "test-provider", "approvalPolicy": "never", "sandbox": "read-only"})
			peer.call(t, "turn/start", map[string]any{"threadId": id, "input": []any{map[string]string{"type": "text", "text": "continue the copied conversation"}}})
			select {
			case body := <-peerRequests:
				if !strings.Contains(body, "prompt during running turn") || strings.Contains(body, "reply after snapshot") || (mode == "inherited" && !strings.Contains(body, "inherited live history")) {
					t.Fatalf("peer resumed the wrong snapshot: %.2000s", body)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("peer did not resume the live snapshot")
			}
			peer.waitNotification(t, "turn/completed")
			// Both source and destination remain loaded at this point.
			if _, err := source.selection(context.Background(), id); err != nil {
				t.Fatal(err)
			}
		})
	}
}
