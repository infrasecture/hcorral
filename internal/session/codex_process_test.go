package session

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Own the process group so cancellation and cleanup stop background children
// before testing removes their disposable homes. Killing only the app server
// can leave a plugin clone writing into a directory undergoing removal.
func nativeFixtureCommand(ctx context.Context, binary string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Cancellation and post-Wait cleanup can both reach this path. Signal
	// the owned group once: macOS can return EPERM on a second kill while
	// already-terminated children await reaping by their new parent.
	var stopped sync.Once
	var stopErr error
	cmd.Cancel = func() error {
		stopped.Do(func() {
			stopErr = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			if errors.Is(stopErr, syscall.ESRCH) {
				stopErr = os.ErrProcessDone
			}
		})
		return stopErr
	}
	return cmd
}

func TestNativeFixtureStopsBackgroundChildren(t *testing.T) {
	for _, cancelProcess := range []bool{false, true} {
		name := "graceful parent exit"
		if cancelProcess {
			name = "canceled parent"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// The background child retains stdout even after the parent exits.
			// EOF therefore proves that cleanup reached the child as well.
			cmd := nativeFixtureCommand(ctx, "sh", "-c", "sleep 60 &\nprintf 'ready\\n'\nread -r ignored || exit 0")
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			cmd.Stdout = writer
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Cancel()
			writer.Close()
			if err := reader.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			output := bufio.NewReader(reader)
			if line, err := output.ReadString('\n'); err != nil || line != "ready\n" {
				t.Fatalf("fixture readiness: %q, %v", line, err)
			}
			if cancelProcess {
				cancel()
			} else {
				stdin.Close()
			}
			err = cmd.Wait()
			if !cancelProcess && err != nil {
				t.Fatalf("parent did not exit gracefully: %v", err)
			}
			if err := cmd.Cancel(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Fatal(err)
			}
			if _, err := output.ReadByte(); !errors.Is(err, io.EOF) {
				t.Fatalf("background child retained the fixture pipe: %v", err)
			}
		})
	}
}
