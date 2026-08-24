package legacyguard

import (
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
