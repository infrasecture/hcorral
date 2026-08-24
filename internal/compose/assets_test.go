package compose

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeStableAndProtected(t *testing.T) {
	t.Parallel()
	cache := t.TempDir()
	m := Materializer{CacheHome: cache, Platform: "linux"}
	first, err := m.Materialize()
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Materialize()
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("paths changed: %#v != %#v", first, second)
	}
	for _, path := range []string{first.Base, first.X11, first.Wayland} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %o", path, info.Mode().Perm())
		}
	}
}

func TestMaterializeRejectsSymlinkCacheLeaf(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if err := os.Symlink(t.TempDir(), cache); err != nil {
		t.Fatal(err)
	}
	_, err := (Materializer{CacheHome: cache, Platform: "linux"}).Materialize()
	if err == nil {
		t.Fatal("expected symlink rejection")
	}
}
