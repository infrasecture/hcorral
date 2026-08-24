package compose

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/infrasecture/hcorral/internal/config"
)

func TestNormalizeMount(t *testing.T) {
	t.Parallel()
	got, target, err := normalizeMount("/work/caller", "./cache:/mnt/cache:ro")
	if err != nil {
		t.Fatal(err)
	}
	if got != "/work/caller/cache:/mnt/cache:ro" || target != "/mnt/cache" {
		t.Fatalf("got %q target %q", got, target)
	}
}

func TestNormalizeMountResolvesRelativePathWithSlashButKeepsVolumeName(t *testing.T) {
	t.Parallel()
	pathMount, _, err := normalizeMount("/work/caller", "cache/data:/mnt/data")
	if err != nil {
		t.Fatal(err)
	}
	if pathMount != "/work/caller/cache/data:/mnt/data" {
		t.Fatalf("relative path mount = %q", pathMount)
	}
	volumeMount, _, err := normalizeMount("/work/caller", "team_state:/mnt/state:ro")
	if err != nil || volumeMount != "team_state:/mnt/state:ro" {
		t.Fatalf("named volume mount = %q, %v", volumeMount, err)
	}
}

func TestExtraMountOverlayDeclaresNamedVolumeExternal(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	cfg := config.Config{CallerDir: "/work", Workspace: "/work", ContainerHome: "/home/alice", Workdir: "/work", ExtraVolumes: []string{"team_state:/mnt/state:ro"}}
	generated, err := ExtraMountOverlay(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer generated.Cleanup()
	content, err := os.ReadFile(generated.Path)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatal(err)
	}
	logical := extraVolumeLogicalName("team_state")
	definition := document["volumes"].(map[string]any)[logical].(map[string]any)
	if definition["external"] != true || definition["name"] != "team_state" {
		t.Fatalf("external definition = %#v", definition)
	}
}

func TestExtraMountOverlayRejectsSymlinkedGeneratedDirectory(t *testing.T) {
	root := t.TempDir()
	cache := filepath.Join(root, "cache")
	if err := os.Mkdir(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(cache, "hcorral")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", cache)
	cfg := config.Config{CallerDir: "/work", Workspace: "/work", ContainerHome: "/home/alice", Workdir: "/work", ExtraVolumes: []string{"team_state:/mnt/state"}}
	if _, err := ExtraMountOverlay(cfg); err == nil {
		t.Fatal("symlinked generated overlay directory was accepted")
	}
}

func TestPathsOverlap(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		left, right string
		want        bool
	}{
		{"/a", "/a", true}, {"/a/b", "/a", true}, {"/a", "/a/b", true}, {"/a", "/ab", false},
	} {
		if got := pathsOverlap(test.left, test.right); got != test.want {
			t.Errorf("pathsOverlap(%q,%q)=%v", test.left, test.right, got)
		}
	}
}
