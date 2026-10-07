package session

import (
	"context"
	"encoding/json"
	"errors"
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

func promotionProvider(t *testing.T, home *Home) <-chan string {
	t.Helper()
	requests := make(chan string, 4)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		fmt.Fprint(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_promotion\"}}\n\n")
		fmt.Fprint(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_promotion\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n")
	}))
	t.Cleanup(provider.Close)
	config := fmt.Sprintf("model='fixture-model'\nmodel_provider='test-provider'\n[model_providers.test-provider]\nname='Local fixture'\nbase_url=%q\nwire_api='responses'\nrequires_openai_auth=false\n", provider.URL+"/v1")
	writeFixture(t, home, "config.toml", []byte(config))
	return requests
}

func nativeParentAndChild(t *testing.T, source *Home, revert, archived bool) (string, []byte) {
	t.Helper()
	prefix := nativeFixture(t, threadA, "paginated", nil, "inherited promotion message")
	tail, err := json.Marshal(map[string]any{"timestamp": "2026-10-06T12:34:57Z", "ordinal": 3, "type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "private parent continuation"}}}})
	if err != nil {
		t.Fatal(err)
	}
	parentPath := fixturePath(threadA, threadA)
	if archived && !revert {
		parentPath = "archived_sessions/" + filepath.Base(parentPath)
	}
	writeFixture(t, source, parentPath+".zst", append(append(append([]byte(nil), prefix...), tail...), '\n'))
	id, rollout := threadB, threadB
	if revert {
		id, rollout = threadA, rolloutA
	}
	path := fixturePath(id, rollout)
	if archived && revert {
		path = "archived_sessions/" + filepath.Base(path)
	}
	base := &HistoryPosition{RolloutID: threadA, EndOrdinalExclusive: 3, EndByteOffset: uint64(len(prefix))}
	writeFixture(t, source, path, nativeFixture(t, id, "paginated", base, "dependent promotion message"))
	if revert {
		db := fixtureDB(t, source, true)
		if _, err := db.Exec("INSERT INTO threads VALUES (?, ?, ?, 'paginated')", id, filepath.Join(source.Path, path), archived); err != nil {
			t.Fatal(err)
		}
	}
	return id, prefix
}

func resumePromotion(t *testing.T, server *codexServer, requests <-chan string, workspace, id string, fullParent bool) {
	t.Helper()
	server.notifications = nil
	server.call(t, "thread/resume", map[string]any{"threadId": id, "cwd": workspace, "modelProvider": "test-provider", "approvalPolicy": "never", "sandbox": "read-only"})
	server.call(t, "turn/start", map[string]any{"threadId": id, "input": []any{map[string]string{"type": "text", "text": "verify promoted history"}}})
	select {
	case body := <-requests:
		if !strings.Contains(body, "inherited promotion message") || strings.Contains(body, "private parent continuation") != fullParent || strings.Contains(body, "dependent promotion message") == fullParent {
			t.Fatalf("native resumed the wrong inherited scope: %.2000s", body)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("native Codex did not send resumed history")
	}
	server.waitNotification(t, "turn/completed")
}

func TestCodexPromotesInheritedParent(t *testing.T) {
	binary := os.Getenv("HCORRAL_TEST_CODEX")
	if binary == "" {
		t.Skip("set HCORRAL_TEST_CODEX for native promotion qualification")
	}
	for _, archived := range []bool{false, true} {
		for _, compressed := range []bool{false, true} {
			t.Run(fmt.Sprintf("archived=%v/compressed=%v", archived, compressed), func(t *testing.T) {
				source, destination := fixtureHome(t), fixtureHome(t)
				workspace := t.TempDir()
				id, prefix := nativeParentAndChild(t, source, false, archived)
				requests := promotionProvider(t, destination)
				child := published(t, received(t, destination, exported(t, source, id)))
				if compressed {
					path := child.Files[0].Path
					writeFixture(t, destination, path+".zst", prefix)
					if err := os.Remove(filepath.Join(destination.Path, path)); err != nil {
						t.Fatal(err)
					}
				}
				server := startCodex(t, binary, destination.Path, workspace)
				server.call(t, "thread/list", map[string]any{"limit": 100})
				before, err := destination.selection(context.Background(), threadA)
				if err != nil || before == nil || !strings.Contains(before.path, prerequisiteRoot) {
					t.Fatalf("native indexing did not select the prerequisite: %+v %v", before, err)
				}
				parent := published(t, received(t, destination, exported(t, source, threadA)))
				if !parent.Files[0].Promoted || !parent.SelectionRepaired || parent.Archived != archived {
					t.Fatalf("missing complete-parent promotion: %+v", parent)
				}
				after, err := destination.selection(context.Background(), threadA)
				if err != nil || after == nil || after.path != filepath.Join(destination.Path, parent.MainPath) || after.archived != archived {
					t.Fatalf("native selection was not repaired: %+v %v", after, err)
				}
				if plan := inspect(t, destination, threadA); len(plan.Files) != 1 || plan.Files[0].Prefix {
					t.Fatal("promoted complete parent cannot be re-exported")
				}
				if plan := inspect(t, destination, id); plan.Files[0].SHA256 != hash(prefix) {
					t.Fatal("promotion changed the child's inherited boundary")
				}
				again := published(t, received(t, destination, exported(t, source, threadA)))
				if again.Files[0].Created || again.Files[0].Promoted || again.SelectionRepaired {
					t.Fatalf("repeat promotion was not a no-op: %+v", again)
				}
				resumePromotion(t, server, requests, workspace, id, false)
				if archived {
					server.call(t, "thread/unarchive", map[string]string{"threadId": threadA})
				}
				resumePromotion(t, server, requests, workspace, threadA, true)
				// Native lineage lookup may choose either representation of a
				// rollout. A NEW native fork after promotion must inherit the full
				// parent's continuation even if it finds the old managed location.
				forked := server.call(t, "thread/fork", map[string]any{"threadId": threadA, "cwd": workspace, "modelProvider": "test-provider", "approvalPolicy": "never", "sandbox": "read-only"})
				var fork struct {
					Thread struct {
						ID string `json:"id"`
					} `json:"thread"`
				}
				if err := json.Unmarshal(forked, &fork); err != nil || fork.Thread.ID == "" || fork.Thread.ID == threadA {
					t.Fatalf("native fork failed: %s %v", forked, err)
				}
				resumePromotion(t, server, requests, workspace, fork.Thread.ID, true)
				server.finish(t)
			})
		}
	}
}

func TestCodexIndexesDuringPublication(t *testing.T) {
	binary := os.Getenv("HCORRAL_TEST_CODEX")
	if binary == "" {
		t.Skip("set HCORRAL_TEST_CODEX for native indexing qualification")
	}
	for _, interrupted := range []bool{false, true} {
		for _, archived := range []bool{false, true} {
			t.Run(fmt.Sprintf("interrupted=%v/archived=%v", interrupted, archived), func(t *testing.T) {
				source, destination := fixtureHome(t), fixtureHome(t)
				workspace := t.TempDir()
				id, _ := nativeParentAndChild(t, source, true, archived)
				requests := promotionProvider(t, destination)
				stream := exported(t, source, id)
				in := received(t, destination, stream)
				var server *codexServer
				stop := errors.New("interrupted after native index observed prerequisite")
				result, err := in.publish(context.Background(), destination, func(i int) error {
					if i != 0 {
						return nil
					}
					server = startCodex(t, binary, destination.Path, workspace)
					server.call(t, "thread/list", map[string]any{"limit": 100, "archived": true})
					selected, err := destination.selection(context.Background(), id)
					if err != nil || selected == nil || !strings.Contains(selected.path, prerequisiteRoot) {
						t.Fatalf("fixture did not exercise native index overlap: %+v %v", selected, err)
					}
					if interrupted {
						return stop
					}
					return nil
				})
				if interrupted {
					if !errors.Is(err, stop) {
						t.Fatalf("unexpected interruption: %v", err)
					}
					if err := in.Close(); err != nil {
						t.Fatal(err)
					}
					result = published(t, received(t, destination, stream))
				} else if err != nil {
					t.Fatal(err)
				}
				if !result.SelectionRepaired || result.Archived != archived {
					t.Fatalf("index overlap was not repaired: %+v", result)
				}
				selected, err := destination.selection(context.Background(), id)
				if err != nil || selected == nil || selected.path != filepath.Join(destination.Path, result.MainPath) || selected.archived != archived {
					t.Fatalf("native index still points at a prerequisite: %+v %v", selected, err)
				}
				if plan := inspect(t, destination, id); len(plan.Files) != 2 || plan.Files[1].RolloutID != rolloutA {
					t.Fatal("imported revert cannot be re-exported")
				}
				if archived {
					server.call(t, "thread/unarchive", map[string]string{"threadId": id})
				}
				resumePromotion(t, server, requests, workspace, id, false)
				server.finish(t)
			})
		}
	}
}
