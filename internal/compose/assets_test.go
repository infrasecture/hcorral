package compose

import (
	"os"
	"path/filepath"
	"sync"
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

func TestMaterializeConcurrentWritersConverge(t *testing.T) {
	t.Parallel()
	materializer := Materializer{CacheHome: t.TempDir(), Platform: "linux"}
	const workers = 16
	results := make(chan AssetPaths, workers)
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for index := 0; index < workers; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			paths, err := materializer.Materialize()
			if err != nil {
				errors <- err
				return
			}
			results <- paths
		}()
	}
	group.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	var first AssetPaths
	for paths := range results {
		if first == (AssetPaths{}) {
			first = paths
		} else if paths != first {
			t.Fatalf("concurrent materialization diverged: %#v != %#v", paths, first)
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

func TestMaterializeRejectsSymlinkAssetFile(t *testing.T) {
	t.Parallel()
	cache := t.TempDir()
	materializer := Materializer{CacheHome: cache, Platform: "linux"}
	paths, err := materializer.Materialize()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(paths.Base)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.Base); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, paths.Base); err != nil {
		t.Fatal(err)
	}
	if _, err := materializer.Materialize(); err == nil {
		t.Fatal("symlinked cached asset was accepted")
	}
}
