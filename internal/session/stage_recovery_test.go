package session

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestStagingCrashProcess(t *testing.T) {
	mode := os.Getenv("HCORRAL_STAGE_CRASH_PROCESS")
	if mode == "" {
		return
	}
	h, err := OpenHome(os.Getenv("HCORRAL_STAGE_CRASH_HOME"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	ready := func(name string) {
		fmt.Println(name)
		io.Copy(io.Discard, os.Stdin) // Parent holds pipe open until SIGKILL.
		t.Fatal("crash fixture unexpectedly resumed")
	}
	if mode == "creation" {
		guard, err := h.stagingCoordination(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer guard.Close()
		name := stagingPrefix + strings.Repeat("c", 32)
		if err := unix.Mkdirat(int(h.dir.Fd()), name, 0o700); err != nil {
			t.Fatal(err)
		}
		ready(name)
	}
	wire, err := os.Open(os.Getenv("HCORRAL_STAGE_CRASH_STREAM"))
	if err != nil {
		t.Fatal(err)
	}
	defer wire.Close()
	in, err := h.Receive(context.Background(), wire, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if mode == "staged" {
		ready(in.name)
	}
	_, err = in.publish(context.Background(), h, func(i int) error {
		if mode == "partial" && i == 0 {
			ready(in.name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ready(in.name)
}

func TestRecoveryAfterSIGKILLPreservesLiveTransfersAndPublishedFiles(t *testing.T) {
	for _, mode := range []string{"creation", "staged", "partial", "published"} {
		t.Run(mode, func(t *testing.T) {
			src, dst := fixtureHome(t), fixtureHome(t)
			_, child, prefix := fixtureFork(t, src)
			stream := exported(t, src, threadB)
			wire := filepath.Join(t.TempDir(), "stream.tar")
			if err := os.WriteFile(wire, stream, 0o600); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestStagingCrashProcess$")
			cmd.Env = []string{"HCORRAL_STAGE_CRASH_PROCESS=" + mode, "HCORRAL_STAGE_CRASH_HOME=" + dst.Path, "HCORRAL_STAGE_CRASH_STREAM=" + wire}
			cmd.WaitDelay = time.Second
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
			scanner := bufio.NewScanner(stdout)
			if !scanner.Scan() || !stagingName(scanner.Text()) {
				t.Fatalf("crash fixture did not reach its boundary: %q %v", scanner.Text(), scanner.Err())
			}
			orphan := filepath.Join(dst.Path, scanner.Text())
			if mode == "creation" {
				blocked, stop := context.WithTimeout(ctx, 50*time.Millisecond)
				defer stop()
				if in, err := dst.Receive(blocked, bytes.NewReader(stream), DefaultLimits()); !errors.Is(err, context.DeadlineExceeded) {
					if in != nil {
						in.Close()
					}
					t.Fatalf("unleased live creation was not coordinated: %v", err)
				}
			} else {
				old := time.Unix(1, 0) // Age cannot make a live transfer an orphan.
				if err := os.Chtimes(orphan, old, old); err != nil {
					t.Fatal(err)
				}
				concurrent := received(t, dst, stream)
				if err := concurrent.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := os.Stat(orphan); err != nil {
				t.Fatal("recovery removed live staging")
			}
			var livePaths []string
			var liveInodes []os.FileInfo
			inventory, err := dst.inventory(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, file := range inventory {
				path := filepath.Join(dst.Path, file.path)
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				livePaths, liveInodes = append(livePaths, path), append(liveInodes, info)
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL || ctx.Err() != nil {
				t.Fatalf("child was not killed at its boundary: %v %s", err, stderr.String())
			}
			if _, err := os.Stat(orphan); err != nil {
				t.Fatal("fixture did not leave abandoned staging")
			}
			retry := received(t, dst, stream)
			if _, err := os.Stat(orphan); !os.IsNotExist(err) {
				t.Fatalf("retry retained recognized orphan: %v", err)
			}
			for i, path := range livePaths {
				after, err := os.Stat(path)
				if err != nil || !os.SameFile(liveInodes[i], after) {
					t.Fatalf("recovery changed published history: %v", err)
				}
			}
			result := published(t, retry)
			for i, expected := range [][]byte{prefix, child} {
				data, err := os.ReadFile(filepath.Join(dst.Path, result.Files[i].Path))
				if err != nil || !bytes.Equal(data, expected) {
					t.Fatal("retry lost history after a killed transfer")
				}
			}
			if err := retry.Close(); err != nil {
				t.Fatal(err)
			}
			assertNoStaging(t, dst)
		})
	}
}

func TestRecoveryPreservesUnknownAndUnsafeStaging(t *testing.T) {
	for _, kind := range []string{"old version", "future version", "bad nonce", "unknown file", "symlink", "fifo", "directory", "missing lease", "linked lease", "changed lease", "public directory", "inaccessible directory", "foreign owner"} {
		t.Run(kind, func(t *testing.T) {
			h := fixtureHome(t)
			name := stagingPrefix + strings.Repeat("a", 32)
			switch kind {
			case "old version":
				name = ".hcorral-transfer-" + strings.Repeat("a", 32)
			case "future version":
				name = ".hcorral-transfer-v2-" + strings.Repeat("a", 32)
			case "bad nonce":
				name = stagingPrefix + "not-a-valid-nonce"
			}
			path := filepath.Join(h.Path, name)
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
			lease := filepath.Join(path, stagingLease)
			if err := os.WriteFile(lease, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			payload := filepath.Join(path, "000000.jsonl")
			if err := os.WriteFile(payload, []byte("preserve"), 0o600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "unknown file":
				err = os.WriteFile(filepath.Join(path, "credentials"), []byte("not staging"), 0o600)
			case "symlink":
				err = os.Symlink(t.TempDir(), filepath.Join(path, "000001.jsonl"))
			case "fifo":
				err = unix.Mkfifo(filepath.Join(path, "000001.jsonl"), 0o600)
			case "directory":
				err = os.Mkdir(filepath.Join(path, "000001.jsonl"), 0o700)
			case "missing lease":
				err = os.Remove(lease)
			case "linked lease":
				err = os.Link(lease, filepath.Join(h.Path, "another lease"))
			case "changed lease":
				err = os.WriteFile(lease, []byte("future protocol"), 0o600)
			case "public directory":
				err = os.Chmod(path, 0o755)
			case "inaccessible directory":
				err = os.Chmod(path, 0)
				t.Cleanup(func() { os.Chmod(path, 0o700) })
			case "foreign owner":
				if os.Geteuid() != 0 {
					t.Skip("requires root to construct another owner's directory")
				}
				err = os.Chown(path, 12345, 23456)
			}
			if err != nil {
				t.Fatal(err)
			}
			in, err := h.staging(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if err := in.Close(); err != nil {
				t.Fatal(err)
			}
			if kind == "inaccessible directory" {
				if err := os.Chmod(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if data, err := os.ReadFile(payload); err != nil || string(data) != "preserve" {
				t.Fatalf("recovery touched unfamiliar staging: %v", err)
			}
		})
	}
}

func TestRecoveryHandlesMultipleEnumerationBatches(t *testing.T) {
	h := fixtureHome(t)
	for i := 0; i < 140; i++ {
		name := fmt.Sprintf("%s%032x", stagingPrefix, i)
		path := filepath.Join(h.Path, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, stagingLease), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			for slot := 0; slot < 260; slot++ {
				if err := os.WriteFile(filepath.Join(path, fmt.Sprintf("extension-%06d", slot)), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	in, err := h.staging(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	assertNoStaging(t, h)
}
