package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/infrasecture/hcorral/internal/session"
)

// Run the real command entrypoint in an isolated subprocess, including its
// signal handler and descriptor cancellation, without building inside the test.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("HCORRAL_SESSION_HELPER_TEST_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"hcorral-session"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func TestSignalCleansStagingWhileInputPipeIsBlocked(t *testing.T) {
	const id = "019a1234-1111-7111-8111-111111111111"
	source := t.TempDir()
	path := filepath.Join(source, "sessions/2026/10/06/rollout-2026-10-06T12-34-56-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"session_meta","payload":{"id":"`+id+`","history_mode":"legacy"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home, err := session.OpenHome(source)
	if err != nil {
		t.Fatal(err)
	}
	defer home.Close()
	snapshot, err := home.Snapshot(context.Background(), id, home, session.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	if err := snapshot.Export(context.Background(), &stream); err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	for _, signal := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			destination := t.TempDir()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestHelperProcess$", "--", "import", "--protocol=1", "--home", destination, "--sqlite-home", destination, "--id", id)
			cmd.Env = []string{"HCORRAL_SESSION_HELPER_TEST_PROCESS=1"}
			cmd.Dir = t.TempDir()
			cmd.WaitDelay = time.Second
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				cmd.Process.Kill()
				cmd.Wait()
			}()
			// Send the manifest and payload, but no completion record or EOF.
			// The process will block waiting for the next input tar header.
			if _, err := io.Copy(stdin, bytes.NewReader(stream.Bytes()[:2048])); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				staged, err := filepath.Glob(filepath.Join(destination, ".hcorral-transfer-*", "000000.jsonl"))
				if err != nil {
					t.Fatal(err)
				}
				if len(staged) == 1 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("helper did not reach payload staging")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err := cmd.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err == nil {
				t.Fatal("interrupted helper reported success")
			}
			if ctx.Err() != nil {
				t.Fatal("signal did not unblock the helper's stdin")
			}
			if stdout.Len() != 0 || !strings.Contains(stderr.String(), "hcorral-session:") {
				t.Fatalf("invalid interrupted-helper response: stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
			entries, err := os.ReadDir(destination)
			if err != nil || len(entries) != 1 || entries[0].Name() != ".hcorral-staging.lock" {
				t.Fatalf("signal left partial publication or staging: %v %v", entries, err)
			}
		})
	}
}
