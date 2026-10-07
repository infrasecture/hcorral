package session

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestStorageInspectionRequiresValidDescriptor(t *testing.T) {
	f, err := storageFile(-1, "invalid")
	if f != nil || !errors.Is(err, unix.EBADF) {
		t.Fatalf("invalid descriptor accepted: %v %v", f, err)
	}
}

func TestStorageInspectionKeepsOpenedIdentity(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, "selected")
	if err := os.Mkdir(name, 0o700); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Replace the pathname before inspecting storage. The descriptor must
	// still refer to the original directory, not its new namesake.
	if err := os.Rename(name, filepath.Join(root, "original")); err != nil {
		unix.Close(fd)
		t.Fatal(err)
	}
	if err := os.Mkdir(name, 0o700); err != nil {
		unix.Close(fd)
		t.Fatal(err)
	}
	f, err := storageFile(fd, name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(filepath.Join(root, "original"))
	if err != nil || !os.SameFile(got, want) {
		t.Fatalf("storage inspection changed descriptor identity: %v", err)
	}
}
