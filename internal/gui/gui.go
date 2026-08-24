package gui

import (
	"context"
	"fmt"
	"os"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/compose"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
)

type Selection struct {
	Mode string
	File string
	Env  map[string]string
}

type Resolver struct {
	Runner  command.Runner
	Environ func(string) string
	UID     int
}

func NewResolver(runner command.Runner) Resolver {
	return Resolver{Runner: runner, Environ: os.Getenv, UID: os.Getuid()}
}

func (r Resolver) Resolve(ctx context.Context, intent config.GUIIntent, workspace identity.Workspace, assets compose.AssetPaths) (Selection, error) {
	if !intent.Specified || intent.Mode == "none" {
		return Selection{Mode: "none", Env: map[string]string{"HCORRAL_GUI_MODE": "none"}}, nil
	}
	return r.resolvePlatform(ctx, intent.Mode, workspace, assets)
}

func unsupported(mode string) (Selection, error) {
	return Selection{}, fmt.Errorf("GUI mode %q is supported only on Linux", mode)
}
