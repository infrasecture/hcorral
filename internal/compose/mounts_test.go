package compose

import "testing"

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
