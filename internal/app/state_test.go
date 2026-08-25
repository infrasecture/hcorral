package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

type volumeInspectionRunner struct {
	result command.Result
	err    error
}

func (r volumeInspectionRunner) Capture(_ context.Context, argv, _ []string) (command.Result, error) {
	if strings.Join(argv, " ") != "docker volume inspect private-state" {
		return command.Result{}, errors.New("unexpected command")
	}
	return r.result, r.err
}

func (volumeInspectionRunner) Run(context.Context, []string, []string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("unexpected mutation")
}

func (volumeInspectionRunner) Replace([]string, []string) error {
	return errors.New("unexpected replace")
}

func TestValidateStateOwnershipIsReadOnlyAndRequiresFullPrivateLabels(t *testing.T) {
	t.Parallel()
	workspace := identity.Workspace{FullID: strings.Repeat("a", 64), Project: "hcorral-demo-aaaaaaa"}
	cfg := config.Config{StateMode: config.StateCustom, StateVolumeName: "private-state"}
	foreign := `[{"Name":"private-state","Labels":{"ai.infrasecture.hcorral.workspace-id":"` + strings.Repeat("b", 64) + `"}}]`
	docker := containerruntime.NewDocker(volumeInspectionRunner{result: command.Result{Stdout: []byte(foreign)}})
	if err := validateStateOwnership(context.Background(), docker, cfg, workspace); err == nil {
		t.Fatal("custom volume with conflicting hcorral ownership was accepted")
	}

	cfg.StateMode, cfg.StateVolumeName = config.StatePrivate, "private-state"
	unlabelled := `[{"Name":"private-state","Labels":{}}]`
	docker = containerruntime.NewDocker(volumeInspectionRunner{result: command.Result{Stdout: []byte(unlabelled)}})
	if err := validateStateOwnership(context.Background(), docker, cfg, workspace); err == nil {
		t.Fatal("unlabelled private volume was accepted")
	}
}

func TestPlanManagedStateRemovalRefusesReferencedWorkspaceVolume(t *testing.T) {
	t.Parallel()
	workspace := identity.Workspace{
		FullID:  strings.Repeat("a", 64),
		ShortID: "aaaaaaa",
		Slug:    "demo",
		Project: "hcorral-demo-bbbbbbb",
	}
	name := identity.WorkspaceVolumeName(workspace)
	volume := containerruntime.Volume{Name: name, Labels: identity.PrivateVolumeLabels(workspace)}
	runner := &fakeRunner{volumes: map[string]containerruntime.Volume{name: volume}}
	docker := containerruntime.NewDocker(runner)
	cfg := config.Config{StateMode: config.StatePrivate}

	selected := containerruntime.Container{Name: "/" + workspace.Project, Mounts: []containerruntime.Mount{{Type: "volume", Name: name}}}
	selected.Config.Labels = map[string]string{"com.docker.compose.project": workspace.Project}
	other := containerruntime.Container{Name: "/hcorral-demo-explicit", Mounts: []containerruntime.Mount{{Type: "volume", Name: name}}}
	other.Config.Labels = map[string]string{"com.docker.compose.project": "hcorral-demo-explicit"}

	remove, gotName, err := planManagedStateRemoval(context.Background(), docker, cfg, workspace, []containerruntime.Container{selected, other})
	if err == nil || !strings.Contains(err.Error(), "hcorral-demo-explicit") {
		t.Fatalf("referenced workspace volume error = %v, want named preflight refusal", err)
	}
	if remove || gotName != name {
		t.Fatalf("remove=%v name=%q, want refusal for %q", remove, gotName, name)
	}

	remove, gotName, err = planManagedStateRemoval(context.Background(), docker, cfg, workspace, []containerruntime.Container{selected})
	if err != nil {
		t.Fatalf("unreferenced workspace volume was rejected: %v", err)
	}
	if !remove || gotName != name {
		t.Fatalf("remove=%v name=%q, want removal %q", remove, gotName, name)
	}
}

var _ command.Runner = volumeInspectionRunner{}
