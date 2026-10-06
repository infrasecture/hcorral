package sessionhelper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infrasecture/hcorral/internal/session"
)

const fixtureID = "019a1234-1111-7111-8111-111111111111"
const fixtureRollout = "sessions/2026/10/06/rollout-2026-10-06T12-34-56-" + fixtureID + ".jsonl"

func helperArgs(operation, home string) []string {
	return []string{operation, "--protocol=1", "--home", home, "--sqlite-home", home, "--id", fixtureID}
}

func helperFixture(t *testing.T) (string, []byte) {
	t.Helper()
	home := t.TempDir()
	data := []byte(`{"type":"session_meta","payload":{"id":"` + fixtureID + `","timestamp":"2026-10-06T12:34:56Z","history_mode":"legacy","cli_version":"0.160.0","cwd":"/project with spaces"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"saved conversation"}]}}` + "\n")
	path := filepath.Join(home, fixtureRollout)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return home, data
}

func TestHelpersStreamConversationWithoutShellOrRuntime(t *testing.T) {
	source, data := helperFixture(t)
	destination := filepath.Join(t.TempDir(), "new destination with spaces")
	reader, writer := io.Pipe()
	sent := make(chan error, 1)
	go func() {
		err := Run(context.Background(), helperArgs("export", source), nil, writer)
		writer.CloseWithError(err)
		sent <- err
	}()
	var response bytes.Buffer
	err := Run(context.Background(), helperArgs("import", destination), reader, &response)
	reader.CloseWithError(err)
	if senderErr := <-sent; err != nil || senderErr != nil {
		t.Fatalf("export=%v import=%v", senderErr, err)
	}
	var result session.Result
	if err := json.Unmarshal(response.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ThreadID != fixtureID || result.MainPath != fixtureRollout || len(result.Files) != 1 || !result.Files[0].Created {
		t.Fatalf("invalid helper response: %+v", result)
	}
	got, err := os.ReadFile(filepath.Join(destination, fixtureRollout))
	if err != nil || !bytes.Equal(data, got) {
		t.Fatalf("helper changed history: %v", err)
	}
	for _, name := range []string{"auth.json", "config.toml", "state_5.sqlite"} {
		if _, err := os.Stat(filepath.Join(destination, name)); !os.IsNotExist(err) {
			t.Fatalf("helper initialized unrelated %s", name)
		}
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".hcorral-transfer-") {
			t.Fatal("helper left staging behind")
		}
	}
}

func TestHelperPreflightRejectsInvalidRequestsWithoutCreatingHome(t *testing.T) {
	for _, problem := range []string{"operation", "protocol", "id", "relative", "sqlite home", "limits", "extra", "cancelled", "missing source"} {
		t.Run(problem, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "must-not-exist")
			args := helperArgs("import", home)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch problem {
			case "operation":
				args[0] = "start"
			case "protocol":
				args[1] = "--protocol=2"
			case "id":
				args[len(args)-1] = "../escape"
			case "relative":
				args[3] = "relative"
			case "sqlite home":
				args[5] = ""
			case "limits":
				args = append(args, "--record-bytes=0")
			case "extra":
				args = append(args, "unexpected")
			case "cancelled":
				cancel()
			case "missing source":
				args[0] = "export"
			}
			if err := Run(ctx, args, strings.NewReader(""), io.Discard); err == nil {
				t.Fatal("accepted invalid request")
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatal("preflight initialized a home")
			}
		})
	}
}

func TestHelperRefusesDifferentIncomingThreadBeforePublication(t *testing.T) {
	source, _ := helperFixture(t)
	var stream bytes.Buffer
	if err := Run(context.Background(), helperArgs("export", source), nil, &stream); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	args := helperArgs("import", destination)
	args[len(args)-1] = "019a1234-2222-7222-8222-222222222222"
	if err := Run(context.Background(), args, &stream, io.Discard); err == nil || !strings.Contains(err.Error(), "different thread") {
		t.Fatalf("got %v", err)
	}
	entries, err := os.ReadDir(destination)
	if err != nil || len(entries) != 0 {
		t.Fatalf("wrong-thread import changed destination: %v %v", entries, err)
	}
}

func TestHelperPropagatesTransportFailure(t *testing.T) {
	source, _ := helperFixture(t)
	disconnected := errors.New("transport disconnected")
	err := Run(context.Background(), helperArgs("export", source), nil, failedWriter{disconnected})
	if !errors.Is(err, disconnected) {
		t.Fatalf("lost transport error: %v", err)
	}
	// Failure must release source guards, allowing an immediate retry.
	if err := Run(context.Background(), helperArgs("export", source), nil, io.Discard); err != nil {
		t.Fatalf("failed export left a writer guard: %v", err)
	}
}

type failedWriter struct{ err error }

func (w failedWriter) Write([]byte) (int, error) { return 0, w.err }

func TestHelperProtocolNeedsNoHome(t *testing.T) {
	var output bytes.Buffer
	if err := Run(context.Background(), []string{"protocol"}, nil, &output); err != nil {
		t.Fatal(err)
	}
	var got Capabilities
	if err := json.Unmarshal(output.Bytes(), &got); err != nil || got.Protocol != session.ProtocolVersion || got.OS == "" || got.Arch == "" {
		t.Fatalf("invalid capabilities: %+v %v", got, err)
	}
}
