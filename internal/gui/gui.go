package gui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/compose"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
)

type Selection struct {
	Mode      string
	File      string
	Env       map[string]string
	Reason    string
	authority []byte
	stateHome string
	project   string
}

type Resolver struct {
	Runner  command.Runner
	Environ func(string) string
	UID     int
}

func NewResolver(runner command.Runner) Resolver {
	return Resolver{Runner: runner, Environ: os.Getenv, UID: os.Geteuid()}
}

func (r Resolver) Resolve(ctx context.Context, intent config.GUIIntent, workspace identity.Workspace, assets compose.AssetPaths) (Selection, error) {
	selection, err := r.Discover(ctx, intent, workspace, assets)
	if err != nil {
		return selection, err
	}
	return selection, r.Prepare(ctx, selection)
}

// Discover reads desktop/daemon facts without writing credentials. An automatic
// default is best effort; an explicit request must be satisfied or explained.
func (r Resolver) Discover(ctx context.Context, intent config.GUIIntent, workspace identity.Workspace, assets compose.AssetPaths) (Selection, error) {
	if intent.Mode == "none" {
		return headless("disabled explicitly"), nil
	}
	mode := intent.Mode
	if !intent.Specified {
		mode = "auto"
		if r.Environ("SSH_CONNECTION") != "" || r.Environ("SSH_CLIENT") != "" || r.Environ("SSH_TTY") != "" {
			return headless("automatic desktop forwarding is disabled over SSH"), nil
		}
		if r.Environ("WAYLAND_DISPLAY") == "" && r.Environ("DISPLAY") == "" {
			return headless("no local desktop display"), nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	selection, err := r.resolvePlatform(ctx, mode, workspace, assets)
	if err != nil && !intent.Specified {
		return headless(err.Error()), nil
	}
	return selection, err
}

func headless(reason string) Selection {
	return Selection{Mode: "none", Env: map[string]string{"HCORRAL_GUI_MODE": "none"}, Reason: reason}
}

func unsupported(mode string) (Selection, error) {
	return Selection{}, fmt.Errorf("GUI mode %q is supported only on Linux", mode)
}
