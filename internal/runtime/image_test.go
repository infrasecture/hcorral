package runtime

import "testing"

func TestLatestReferencePolicy(t *testing.T) {
	for ref, want := range map[string]bool{
		"image": true, "image:latest": true, "registry:5000/team/image": true,
		"registry:5000/team/image:latest": true, "image:stable": false,
		"image:1.2.3": false, "image@sha256:abc": false,
		"image:latest@sha256:abc": false, "sha256:abc": false, "": false,
	} {
		if got := IsLatest(ref); got != want {
			t.Errorf("IsLatest(%q)=%v, want %v", ref, got, want)
		}
	}
}
