package command

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestEnvironmentWithoutCompose(t *testing.T) {
	t.Parallel()
	input := []string{"PATH=/bin", "COMPOSE_FILE=bad", "DOCKER_HOST=unix:///x", "COMPOSE_PROFILES=x"}
	want := []string{"PATH=/bin", "DOCKER_HOST=unix:///x"}
	if got := EnvironmentWithoutCompose(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered environment = %#v, want %#v", got, want)
	}
}

func TestExecRunnerRetainsCancellationOfRunningChild(t *testing.T) {
	for _, method := range []string{"capture", "stream"} {
		t.Run(method, func(t *testing.T) {
			ready := filepath.Join(t.TempDir(), "ready")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			argv := []string{"sh", "-c", `printf stdout; printf stderr >&2; printf ready > "$1"; exec sleep 60`, "sh", ready}
			type completion struct {
				result Result
				err    error
			}
			done := make(chan completion, 1)
			go func() {
				if method == "capture" {
					result, err := (ExecRunner{}).Capture(ctx, argv, os.Environ())
					done <- completion{result, err}
				} else {
					var out, stderr bytes.Buffer
					err := (ExecRunner{}).Run(ctx, argv, os.Environ(), nil, &out, &stderr)
					done <- completion{Result{out.Bytes(), stderr.Bytes()}, err}
				}
			}()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(ready); err == nil {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("child did not signal readiness")
				}
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
			select {
			case got := <-done:
				var exit *exec.ExitError
				if !errors.Is(got.err, context.Canceled) || !errors.As(got.err, &exit) {
					t.Fatalf("lost cancellation or child exit: %v", got.err)
				}
				if string(got.result.Stdout) != "stdout" || string(got.result.Stderr) != "stderr" {
					t.Fatalf("lost child output: %+v", got.result)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled child did not finish")
			}
		})
	}
}

func TestCommandErrorPreservesSuccessAndDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if err := commandError(ctx, nil); err != nil {
		t.Fatalf("successful command became failure: %v", err)
	}
	failure := errors.New("child failed")
	if err := commandError(ctx, failure); !errors.Is(err, failure) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost error classification: %v", err)
	}
	if err := commandError(context.Background(), failure); err != failure {
		t.Fatal("ordinary command failure changed")
	}
}

func TestExecRunnerPreservesArgumentsEnvironmentAndStdio(t *testing.T) {
	t.Parallel()
	argv := []string{"sh", "-c", `printf '%s\0' "$@"; printf '%s' "$HCORRAL_TEST_VALUE" >&2; cat`, "sh", "space arg", "line\nbreak", ""}
	env := []string{"PATH=/usr/bin:/bin", "HCORRAL_TEST_VALUE=environment value"}
	input := bytes.NewBufferString("stdin value")
	var stdout, stderr bytes.Buffer
	if err := (ExecRunner{}).Run(context.Background(), argv, env, input, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	wantPrefix := []byte("space arg\x00line\nbreak\x00\x00")
	if !bytes.HasPrefix(stdout.Bytes(), wantPrefix) || !bytes.HasSuffix(stdout.Bytes(), []byte("stdin value")) {
		t.Fatalf("stdout did not preserve argv/stdin: %q", stdout.Bytes())
	}
	if stderr.String() != "environment value" {
		t.Fatalf("stderr/environment = %q", stderr.String())
	}
}
