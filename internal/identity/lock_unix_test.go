//go:build linux || darwin

package identity

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAcquireLockSerializesAndProtectsFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", root)
	first, err := AcquireLock("hcorral-demo-aaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	info, err := os.Stat(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode = %o", info.Mode().Perm())
	}
	acquired := make(chan *Lock, 1)
	errors := make(chan error, 1)
	go func() {
		second, lockErr := AcquireLock("hcorral-demo-aaaaaaa")
		if lockErr != nil {
			errors <- lockErr
			return
		}
		acquired <- second
	}()
	select {
	case second := <-acquired:
		second.Close()
		t.Fatal("second lock acquired before first was released")
	case err := <-errors:
		t.Fatal(err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case second := <-acquired:
		if err := second.Close(); err != nil {
			t.Fatal(err)
		}
	case err := <-errors:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		t.Fatal("second lock did not acquire after release")
	}
}

func TestAcquireLockRejectsSymlinkDirectory(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, "hcorral")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", root)
	if _, err := AcquireLock("hcorral-demo-aaaaaaa"); err == nil {
		t.Fatal("symlinked lock directory was accepted")
	}
}
