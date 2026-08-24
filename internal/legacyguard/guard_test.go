package legacyguard

import (
	"strings"
	"testing"

	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

func TestFindRequiresBindAndIndependentMarker(t *testing.T) {
	t.Parallel()
	legacy := containerruntime.Container{Name: "/demo-codex"}
	legacy.Config.Labels = map[string]string{"io.infrasecture.mycodex.gui": "none"}
	legacy.Mounts = []containerruntime.Mount{{Type: "bind", Source: "/work/demo", Destination: "/work/demo"}}
	legacy.State.Status = "running"
	if got := Find([]containerruntime.Container{legacy}, "/work/demo"); got == nil || got.Container != "demo-codex" {
		t.Fatalf("match = %#v", got)
	}
	if got := Find([]containerruntime.Container{legacy}, "/work/other"); got != nil {
		t.Fatalf("foreign match = %#v", got)
	}

	foreign := legacy
	foreign.Name = "/demo"
	foreign.Config.Labels = map[string]string{}
	if got := Find([]containerruntime.Container{foreign}, "/work/demo"); got != nil {
		t.Fatalf("unmarked match = %#v", got)
	}
}

func TestFindFailsClosedOnAmbiguousSameWorkspaceEvidence(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*containerruntime.Container){
		"name only": func(container *containerruntime.Container) {
			container.Name = "/demo-codex"
		},
		"service only": func(container *containerruntime.Container) {
			container.Config.Labels["com.docker.compose.service"] = "codex"
		},
		"image only": func(container *containerruntime.Container) {
			container.Config.Image = "ghcr.io/infrasecture/harness-workstation:latest"
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			container := containerruntime.Container{Name: "/unrelated"}
			container.Config.Labels = map[string]string{}
			container.Mounts = []containerruntime.Mount{{Type: "bind", Source: "/work/demo", Destination: "/work/demo"}}
			container.State.Status = "exited"
			mutate(&container)

			got := Find([]containerruntime.Container{container}, "/work/demo")
			if got == nil || !strings.HasPrefix(got.Reason, "ambiguous myCodex evidence:") {
				t.Fatalf("ambiguous match = %#v", got)
			}
		})
	}
}

func TestFindIgnoresForeignAndVolumeOnlyResidue(t *testing.T) {
	t.Parallel()
	legacy := containerruntime.Container{Name: "/demo-codex"}
	legacy.Config.Labels = map[string]string{"com.docker.compose.service": "codex"}
	legacy.Mounts = []containerruntime.Mount{
		{Type: "bind", Source: "/work/other", Destination: "/work/other"},
		{Type: "volume", Name: "demo-home", Destination: "/home/demo"},
	}
	if got := Find([]containerruntime.Container{legacy}, "/work/demo"); got != nil {
		t.Fatalf("foreign or volume-only match = %#v", got)
	}
}
