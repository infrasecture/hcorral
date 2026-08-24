package app

import (
	"context"
	"fmt"

	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

func stateVolumeName(cfg config.Config, workspace identity.Workspace) string {
	if cfg.StateVolumeName != "" {
		return cfg.StateVolumeName
	}
	if cfg.StateMode == config.StatePrivate {
		return workspace.Project
	}
	return "hcorral_state"
}

func ensureState(ctx context.Context, docker containerruntime.Docker, cfg config.Config, workspace identity.Workspace) error {
	name := stateVolumeName(cfg, workspace)
	volume, err := docker.InspectVolume(ctx, name)
	if err != nil {
		return err
	}
	switch cfg.StateMode {
	case config.StateCustom:
		if volume == nil {
			return docker.CreateVolume(ctx, name, nil)
		}
		if owner := volume.Labels[identity.LabelWorkspaceID]; owner != "" && owner != workspace.FullID {
			return fmt.Errorf("custom volume %s carries conflicting hcorral workspace ID %s", name, owner)
		}
		return nil
	case config.StatePrivate:
		if volume == nil {
			return docker.CreateVolume(ctx, name, identity.PrivateVolumeLabels(workspace))
		}
		if volume.Labels[identity.LabelWorkspaceID] != workspace.FullID || volume.Labels[identity.LabelStateKind] != "private" || volume.Labels[identity.LabelRuntimeSchema] != identity.RuntimeSchemaVersion {
			return fmt.Errorf("private state volume %s does not have exact ownership labels for workspace %s", name, workspace.FullID)
		}
		return nil
	case config.StateShared:
		if volume == nil {
			return docker.CreateVolume(ctx, name, identity.SharedVolumeLabels())
		}
		if volume.Labels[identity.LabelStateKind] != "shared" || volume.Labels[identity.LabelRuntimeSchema] != identity.RuntimeSchemaVersion {
			return fmt.Errorf("shared state volume %s does not have exact hcorral shared-state labels", name)
		}
		return nil
	default:
		return fmt.Errorf("unsupported state mode %q", cfg.StateMode)
	}
}

func planManagedStateRemoval(ctx context.Context, docker containerruntime.Docker, cfg config.Config, workspace identity.Workspace, containers []containerruntime.Container) (bool, string, error) {
	if cfg.StateMode == config.StateCustom {
		return false, stateVolumeName(cfg, workspace), nil
	}
	name := stateVolumeName(cfg, workspace)
	volume, err := docker.InspectVolume(ctx, name)
	if err != nil || volume == nil {
		return false, name, err
	}
	if cfg.StateMode == config.StatePrivate {
		if volume.Labels[identity.LabelWorkspaceID] != workspace.FullID || volume.Labels[identity.LabelStateKind] != "private" || volume.Labels[identity.LabelRuntimeSchema] != identity.RuntimeSchemaVersion {
			return false, name, fmt.Errorf("refuse to remove private volume %s with mismatched labels", name)
		}
	} else if volume.Labels[identity.LabelStateKind] != "shared" || volume.Labels[identity.LabelRuntimeSchema] != identity.RuntimeSchemaVersion {
		return false, name, fmt.Errorf("refuse to remove shared volume %s with mismatched labels", name)
	}
	for _, container := range containers {
		for _, mount := range container.Mounts {
			if mount.Type == "volume" && mount.Name == name && container.Config.Labels["com.docker.compose.project"] != workspace.Project {
				if cfg.StateMode == config.StateShared {
					return false, name, nil
				}
				return false, name, fmt.Errorf("private state volume %s remains referenced by foreign container %s", name, container.CleanName())
			}
		}
	}
	return true, name, nil
}
