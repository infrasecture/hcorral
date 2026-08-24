//go:build !linux

package gui

import (
	"context"

	"github.com/infrasecture/hcorral/internal/compose"
	"github.com/infrasecture/hcorral/internal/identity"
)

func (r Resolver) resolvePlatform(_ context.Context, mode string, _ identity.Workspace, _ compose.AssetPaths) (Selection, error) {
	return unsupported(mode)
}
