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

var _ command.Runner = volumeInspectionRunner{}
