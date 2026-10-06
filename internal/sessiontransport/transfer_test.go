package sessiontransport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/infrasecture/hcorral/internal/session"
	"github.com/infrasecture/hcorral/internal/sessionhelper"
)

const transferID = "019a1234-1111-7111-8111-111111111111"
const transferRollout = "sessions/2026/10/06/rollout-2026-10-06T12-34-56-" + transferID + ".jsonl"

func transferFixture(t *testing.T) (string, []byte) {
	t.Helper()
	home := t.TempDir()
	data := []byte(`{"type":"session_meta","payload":{"id":"` + transferID + `","timestamp":"2026-10-06T12:34:56Z","history_mode":"legacy","cli_version":"0.160.0","cwd":"/project with spaces"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"saved conversation"}]}}` + "\n")
	file := filepath.Join(home, transferRollout)
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return home, data
}

func transferOptions(operation, host, container string) TransferOptions {
	return TransferOptions{Operation: operation, ThreadID: transferID, HostHome: host, HostSQLiteHome: host, ContainerSQLiteHome: container, Limits: session.DefaultLimits()}
}

func TestTransferReportsCompatiblePrefixExtensions(t *testing.T) {
	testPrefixTransfers(t, sessionhelper.Run)
}

// Exercise result decoding and both endpoint roles. The bundled-helper test
// also runs this against the actual embedded executable, detecting stale assets
// that pass the basic protocol check but still lack compatible-prefix support.
func testPrefixTransfers(t *testing.T, remote helperRun) {
	t.Helper()
	source, destination := t.TempDir(), t.TempDir()
	record := func(kind string, ordinal int, payload any) []byte {
		data, err := json.Marshal(map[string]any{"type": kind, "ordinal": ordinal, "payload": payload})
		if err != nil {
			t.Fatal(err)
		}
		return append(data, '\n')
	}
	parent := record("session_meta", 0, session.Metadata{ThreadID: transferID, HistoryMode: "paginated", Version: "0.160.0"})
	parent = append(parent, record("response_item", 1, map[string]string{"text": "first inherited"})...)
	short := len(parent)
	parent = append(parent, record("response_item", 2, map[string]string{"text": "second inherited"})...)
	long := len(parent)
	parent = append(parent, record("response_item", 3, map[string]string{"text": "private parent tail"})...)
	write := func(id string, data []byte) {
		path := filepath.Join(source, strings.Replace(transferRollout, transferID, id, 1))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(transferID, parent)
	var lastID string
	for i, cutoff := range []int{short, long} {
		id := fmt.Sprintf("019a1234-2222-7222-8222-%012d", i)
		lastID = id
		base := &session.HistoryPosition{RolloutID: transferID, EndOrdinalExclusive: uint64(i + 2), EndByteOffset: uint64(cutoff)}
		write(id, record("session_meta", i+2, session.Metadata{ThreadID: id, HistoryMode: "paginated", Version: "0.160.0", HistoryBase: base}))
		options := transferOptions("import", source, destination)
		options.ThreadID = id
		result, err := transfer(context.Background(), destination, options, remote)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Files) != 2 || result.Files[0].Extended != (i == 1) {
			t.Fatalf("missing prefix result: %+v", result)
		}
		got, err := os.ReadFile(filepath.Join(destination, result.Files[0].Path))
		if err != nil || !bytes.Equal(got, parent[:cutoff]) {
			t.Fatalf("helper copied the wrong ancestor range: %v", err)
		}
	}
	options := transferOptions("export", t.TempDir(), destination)
	options.ThreadID = lastID
	if _, err := transfer(context.Background(), destination, options, remote); err != nil {
		t.Fatalf("helper could not re-export an extended prerequisite: %v", err)
	}
}

func TestTransferCoordinatesBothDirectionsAndStorageAliases(t *testing.T) {
	for _, operation := range []string{"export", "import"} {
		for _, alias := range []bool{false, true} {
			name := operation
			if alias {
				name += "/same-storage"
			}
			t.Run(name, func(t *testing.T) {
				source, data := transferFixture(t)
				destination := filepath.Join(t.TempDir(), "destination with spaces")
				if alias {
					if err := os.Symlink(source, destination); err != nil {
						t.Fatal(err)
					}
				}
				host, container := destination, source
				if operation == "import" {
					host, container = source, destination
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				result, err := transfer(ctx, container, transferOptions(operation, host, container), sessionhelper.Run)
				if err != nil {
					t.Fatal(err)
				}
				if result.ThreadID != transferID || len(result.Files) != 1 || result.Files[0].Created == alias {
					t.Fatalf("unexpected result: %+v", result)
				}
				got, err := os.ReadFile(filepath.Join(destination, transferRollout))
				if err != nil || !bytes.Equal(got, data) {
					t.Fatalf("history changed: %v", err)
				}
				info, err := os.Stat(filepath.Join(destination, transferRollout))
				if err != nil {
					t.Fatal(err)
				}
				stat := info.Sys().(*syscall.Stat_t)
				if int(stat.Uid) != os.Geteuid() || int(stat.Gid) != os.Getegid() || info.Mode().Perm() != 0o600 {
					t.Fatalf("destination has wrong owner or permissions: %v %d:%d", info.Mode(), stat.Uid, stat.Gid)
				}
				for _, home := range []string{source, destination} {
					noStaging(t, home)
				}
				// A successful retry reuses the exact file and does not leak locks.
				result, err = transfer(ctx, container, transferOptions(operation, host, container), sessionhelper.Run)
				if err != nil || result.Files[0].Created {
					t.Fatalf("retry: %+v %v", result, err)
				}
			})
		}
	}
}

func noStaging(t *testing.T, home string) {
	t.Helper()
	entries, err := os.ReadDir(home)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".hcorral-transfer-") {
			t.Fatal("private staging was left behind")
		}
	}
}

func TestExportWaitsForRemoteSuccessBeforePublishing(t *testing.T) {
	source, _ := transferFixture(t)
	destination := t.TempDir()
	failure := errors.New("remote cleanup failed after its complete stream")
	remote := func(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
		if err := sessionhelper.Run(ctx, args, input, output); err != nil {
			return err
		}
		return failure
	}
	result, err := transfer(context.Background(), source, transferOptions("export", destination, source), remote)
	if !errors.Is(err, failure) || result.ThreadID != "" {
		t.Fatalf("lost export error: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(destination, transferRollout)); !os.IsNotExist(err) {
		t.Fatal("published before remote source finalization")
	}
	noStaging(t, destination)
}

func TestImportKeepsConfirmedPublicationWhenRemoteCleanupFails(t *testing.T) {
	source, data := transferFixture(t)
	destination := t.TempDir()
	failure := errors.New("helper container cleanup failed")
	remote := func(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
		if err := sessionhelper.Run(ctx, args, input, output); err != nil {
			return err
		}
		return failure
	}
	result, err := transfer(context.Background(), destination, transferOptions("import", source, destination), remote)
	if !errors.Is(err, failure) || result.ThreadID != transferID || !strings.Contains(err.Error(), "confirmed session publication") {
		t.Fatalf("lost publication result: %+v %v", result, err)
	}
	got, err := os.ReadFile(filepath.Join(destination, transferRollout))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("history: %v", err)
	}
}

func TestTransferSourceFailureDoesNotInitializeDestination(t *testing.T) {
	for _, operation := range []string{"export", "import"} {
		t.Run(operation, func(t *testing.T) {
			source, destination := t.TempDir(), filepath.Join(t.TempDir(), "absent")
			host, container := destination, source
			if operation == "import" {
				host, container = source, destination
			}
			remoteCalled := false
			remote := func(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
				remoteCalled = true
				return sessionhelper.Run(ctx, args, in, out)
			}
			if _, err := transfer(context.Background(), container, transferOptions(operation, host, container), remote); err == nil {
				t.Fatal("accepted missing source thread")
			}
			if operation == "import" && remoteCalled {
				t.Fatal("started remote importer for invalid source")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("initialized destination for invalid source")
			}
		})
	}
}

func TestTransferCancellationUnblocksBothEndpoints(t *testing.T) {
	for _, operation := range []string{"export", "import"} {
		t.Run(operation, func(t *testing.T) {
			source, _ := transferFixture(t)
			destination := t.TempDir()
			host, container := destination, source
			if operation == "import" {
				host, container = source, destination
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			started := make(chan struct{})
			remote := func(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
				close(started)
				if args[0] == "export" {
					if _, err := output.Write([]byte("partial archive")); err != nil {
						return err
					}
				}
				<-ctx.Done()
				return ctx.Err()
			}
			done := make(chan error, 1)
			go func() {
				_, err := transfer(ctx, container, transferOptions(operation, host, container), remote)
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("remote did not start")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation left a blocked transfer")
			}
			noStaging(t, destination)
		})
	}
}

func TestImportWithoutValidAcknowledgementReportsUnknownPublication(t *testing.T) {
	for _, suffix := range []string{"", "garbage", "{}"} {
		t.Run(suffix, func(t *testing.T) {
			source, _ := transferFixture(t)
			destination := t.TempDir()
			remote := func(ctx context.Context, args []string, input io.Reader, output io.Writer) error {
				if err := sessionhelper.Run(ctx, args, input, io.Discard); err != nil {
					return err
				}
				_, err := io.WriteString(output, suffix)
				return err
			}
			result, err := transfer(context.Background(), destination, transferOptions("import", source, destination), remote)
			if err == nil || result.ThreadID != "" || !strings.Contains(err.Error(), "publication status is unknown") {
				t.Fatalf("misreported uncertain import: %+v %v", result, err)
			}
			if _, err := os.Stat(filepath.Join(destination, transferRollout)); err != nil {
				t.Fatal("fixture did not publish")
			}
		})
	}
}

func TestTransferRejectsInvalidArgumentsBeforeCallingEndpoints(t *testing.T) {
	for _, problem := range []string{"operation", "id", "home", "sqlite", "container sqlite", "limits", "cancelled"} {
		t.Run(problem, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "absent")
			options := transferOptions("export", home, "/container/.codex")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch problem {
			case "operation":
				options.Operation = "start"
			case "id":
				options.ThreadID = "../../other"
			case "home":
				options.HostHome = "relative"
			case "sqlite":
				options.HostSQLiteHome = "relative"
			case "container sqlite":
				options.ContainerSQLiteHome = ""
			case "limits":
				options.Limits.Files = 0
			case "cancelled":
				cancel()
			}
			remote := func(context.Context, []string, io.Reader, io.Writer) error {
				t.Error("called remote endpoint for invalid request")
				return nil
			}
			if _, err := transfer(ctx, "/container/.codex", options, remote); err == nil {
				t.Fatal("accepted invalid transfer request")
			}
			if _, err := os.Stat(home); !os.IsNotExist(err) {
				t.Fatal("initialized invalid destination")
			}
		})
	}
}

func TestCompletionOutputIsBounded(t *testing.T) {
	output := &boundedBuffer{maximum: 4, description: "completion result"}
	if n, err := output.Write([]byte("123")); n != 3 || err != nil {
		t.Fatal("rejected bounded response")
	}
	if _, err := output.Write([]byte("45")); err == nil || output.String() != "123" {
		t.Fatal("accepted oversized response")
	}
	// Hide WriterTo so io.Copy would use an inherited bytes.Buffer.ReadFrom
	// if the bounded writer accidentally exposed it.
	reader := struct{ io.Reader }{strings.NewReader("45")}
	if _, err := io.Copy(output, reader); err == nil || output.String() != "123" {
		t.Fatal("io.Copy bypassed the response limit")
	}
}
