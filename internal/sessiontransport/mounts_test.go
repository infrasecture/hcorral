package sessiontransport

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

func TestBindSettingsSurviveInspectionAndMetadataNarrowing(t *testing.T) {
	for _, propagation := range []string{"rprivate", "private", "shared", "rshared", "slave", "rslave"} {
		for _, recursion := range []string{"enabled", "disabled", "writable", "readonly"} {
			t.Run(propagation+"/"+recursion, func(t *testing.T) {
				_, f, _, _ := transportFixture(t)
				// Decode a Docker-shaped response, including fields absent from
				// the top-level Mounts array, rather than setting derived facts.
				options := map[string]any{"Propagation": propagation, "CreateMountpoint": true}
				switch recursion {
				case "disabled":
					options["NonRecursive"] = true
				case "writable":
					options["ReadOnlyNonRecursive"] = true
				case "readonly":
					options["ReadOnlyForceRecursive"] = true
				}
				encoded, err := json.Marshal(map[string]any{"Type": "bind", "Source": "/daemon/workspace", "Target": "/workspace", "Consistency": "consistent", "BindOptions": options})
				if err != nil {
					t.Fatal(err)
				}
				var definition containerruntime.MountDefinition
				if err := json.Unmarshal(encoded, &definition); err != nil {
					t.Fatal(err)
				}
				f.workstation.HostConfig.Mounts = []containerruntime.MountDefinition{definition}
				f.workstation.Mounts[0].Propagation = propagation
				f.workstation.Mounts[0].RW = recursion != "readonly"
				for _, operation := range []string{"import", "export"} {
					database, source := "/workspace/state", "/daemon/workspace/state"
					if recursion == "disabled" {
						if _, err := InspectTargetForTransfer(f.workstation, database, operation); err == nil {
							t.Fatal("narrowing a nonrecursive mount can enter an excluded daemon submount")
						}
						database, source = "/workspace", "/daemon/workspace"
					}
					target, err := InspectTargetForTransfer(f.workstation, database, operation)
					if err != nil {
						t.Fatal(err)
					}
					var selected *containerruntime.Mount
					for i := range target.Mounts {
						if target.Mounts[i].Destination == database {
							selected = &target.Mounts[i]
						}
					}
					if selected == nil || selected.Source != source {
						t.Fatalf("metadata bind not narrowed: %+v", target.Mounts)
					}
					if selected.RW != (operation == "import" && recursion != "readonly") {
						t.Fatal("deployed access was broadened")
					}
					argv := mountArgument(*selected)
					if !strings.Contains(argv, "bind-propagation="+propagation) || strings.Contains(argv, "bind-create") {
						t.Fatalf("incorrect propagation or source creation: %s", argv)
					}
					if recursion != "enabled" && !strings.Contains(argv, "bind-recursive="+recursion) {
						t.Fatalf("discarded recursive settings: %s", argv)
					}
				}
			})
		}
	}
}

func TestUnqualifiedBindSemanticsFailBeforeHelperCreation(t *testing.T) {
	for _, problem := range []string{"private SELinux", "shared SELinux", "cached mode", "delegated consistency", "unknown propagation", "conflicting propagation", "conflicting recursion", "writable forced readonly", "unknown mode"} {
		t.Run(problem, func(t *testing.T) {
			d, f, workspace, _ := transportFixture(t)
			f.workstation.Mounts[1] = containerruntime.Mount{Type: "bind", Source: "/daemon/home", Destination: "/home/actual user", RW: true, Propagation: "rprivate"}
			definition := containerruntime.MountDefinition{Type: "bind", Source: "/daemon/home", Target: "/home/actual user"}
			m := &f.workstation.Mounts[1]
			switch problem {
			case "private SELinux":
				m.Mode = "rw,Z"
			case "shared SELinux":
				m.Mode = "rw,z"
			case "cached mode":
				m.Mode = "rw,cached"
			case "delegated consistency":
				definition.Consistency = "delegated"
			case "unknown propagation":
				m.Propagation = "future"
			case "conflicting propagation":
				definition.BindOptions.Propagation = "rshared"
			case "conflicting recursion":
				definition.BindOptions.ReadOnlyNonRecursive = true
				definition.BindOptions.ReadOnlyForceRecursive = true
			case "writable forced readonly":
				definition.BindOptions.ReadOnlyForceRecursive = true
			case "unknown mode":
				m.Mode = "future-mode"
			}
			f.workstation.HostConfig.Mounts = []containerruntime.MountDefinition{definition}
			if _, err := InspectTarget(f.workstation); err == nil {
				t.Fatal("accepted unqualified storage")
			}
			// An inspected target cannot bypass validation during recheck.
			if err := d.Run(context.Background(), workspace, Target{ContainerID: f.workstation.ID}, []string{"protocol"}, nil, io.Discard, io.Discard); err == nil {
				t.Fatal("created unqualified helper")
			}
			for _, argv := range f.commands {
				if len(argv) > 1 && argv[1] == "create" {
					t.Fatal("created helper before mount refusal")
				}
			}
			// The same unselected workspace mount is not relevant to Codex state.
			m.Destination = "/unrelated"
			f.workstation.HostConfig.Mounts[0].Target = "/unrelated"
			f.workstation.Mounts = append(f.workstation.Mounts, containerruntime.Mount{Type: "volume", Name: "state", Destination: "/home/actual user", RW: true})
			if _, err := InspectTarget(f.workstation); err != nil {
				t.Fatalf("unrelated bind blocked transfer: %v", err)
			}
		})
	}
}

func TestBindSettingChangeAfterHelperCopyIsDetected(t *testing.T) {
	d, f, workspace, _ := transportFixture(t)
	f.workstation.Mounts[1] = containerruntime.Mount{Type: "bind", Source: "/daemon/home", Destination: "/home/actual user", RW: true, Propagation: "rprivate"}
	definition := containerruntime.MountDefinition{Type: "bind", Source: "/daemon/home", Target: "/home/actual user"}
	f.workstation.HostConfig.Mounts = []containerruntime.MountDefinition{definition}
	target, err := InspectTarget(f.workstation)
	if err != nil {
		t.Fatal(err)
	}
	f.changeAfterCopy = func() { f.workstation.HostConfig.Mounts[0].BindOptions.NonRecursive = true }
	err = d.Run(context.Background(), workspace, target, []string{"protocol"}, nil, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("missed mount change: %v", err)
	}
	if f.helper != nil {
		t.Fatal("changed mount left helper behind")
	}
	for _, argv := range f.commands {
		if argv[1] == "start" {
			t.Fatal("started helper with stale mount settings")
		}
	}
}
