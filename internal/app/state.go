package app

import (
	"context"
	"fmt"
	"sort"

	"github.com/infrasecture/hcorral/internal/compose"
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
	if err := validateStateOwnership(ctx, docker, cfg, workspace); err != nil {
		return err
	}
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
		return nil
	case config.StatePrivate:
		if volume == nil {
			return docker.CreateVolume(ctx, name, identity.PrivateVolumeLabels(workspace))
		}
		return nil
	case config.StateShared:
		if volume == nil {
			return docker.CreateVolume(ctx, name, identity.SharedVolumeLabels())
		}
		return nil
	default:
		return fmt.Errorf("unsupported state mode %q", cfg.StateMode)
	}
}

// validateStateOwnership is a read-only preflight. Callers run it before an
// image pull or any other mutation, then ensureState repeats it to close the
// inspection-to-creation race before creating or reusing the selected volume.
func validateStateOwnership(ctx context.Context, docker containerruntime.Docker, cfg config.Config, workspace identity.Workspace) error {
	name := stateVolumeName(cfg, workspace)
	volume, err := docker.InspectVolume(ctx, name)
	if err != nil || volume == nil {
		return err
	}
	switch cfg.StateMode {
	case config.StateCustom:
		if owner := volume.Labels[identity.LabelWorkspaceID]; owner != "" && owner != workspace.FullID {
			return fmt.Errorf("custom volume %s carries conflicting hcorral workspace ID %s", name, owner)
		}
	case config.StatePrivate:
		if volume.Labels[identity.LabelWorkspaceID] != workspace.FullID || volume.Labels[identity.LabelStateKind] != "private" || volume.Labels[identity.LabelRuntimeSchema] != identity.RuntimeSchemaVersion {
			return fmt.Errorf("private state volume %s does not have exact ownership labels for workspace %s", name, workspace.FullID)
		}
	case config.StateShared:
		if volume.Labels[identity.LabelStateKind] != "shared" || volume.Labels[identity.LabelRuntimeSchema] != identity.RuntimeSchemaVersion {
			return fmt.Errorf("shared state volume %s does not have exact hcorral shared-state labels", name)
		}
	default:
		return fmt.Errorf("unsupported state mode %q", cfg.StateMode)
	}
	return nil
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

func validateComposeVolumeOwnership(ctx context.Context, docker containerruntime.Docker, rendered compose.Rendered, workspace identity.Workspace) error {
	for logicalName, definition := range rendered.Volumes {
		if definition.External {
			continue
		}
		if definition.Name == "" {
			return fmt.Errorf("refuse Compose operation: rendered volume %s has no concrete name", logicalName)
		}
		volume, err := docker.InspectVolume(ctx, definition.Name)
		if err != nil {
			return err
		}
		if volume == nil {
			continue
		}
		if volume.Labels["com.docker.compose.project"] != workspace.Project || volume.Labels["com.docker.compose.volume"] != logicalName {
			return fmt.Errorf("refuse Compose operation: volume %s lacks exact Compose ownership labels for %s/%s", definition.Name, workspace.Project, logicalName)
		}
	}
	return nil
}

func validateComposeNetworkOwnership(ctx context.Context, docker containerruntime.Docker, rendered compose.Rendered, workspace identity.Workspace) error {
	for logicalName, definition := range rendered.Networks {
		if definition.External {
			continue
		}
		if definition.Name == "" {
			return fmt.Errorf("refuse Compose operation: rendered network %s has no concrete name", logicalName)
		}
		network, err := docker.InspectNetwork(ctx, definition.Name)
		if err != nil {
			return err
		}
		if network == nil {
			continue
		}
		if network.Labels["com.docker.compose.project"] != workspace.Project || network.Labels["com.docker.compose.network"] != logicalName {
			return fmt.Errorf("refuse Compose operation: network %s lacks exact Compose ownership labels for %s/%s", definition.Name, workspace.Project, logicalName)
		}
	}
	return nil
}

func renderedRemovalTargets(rendered compose.Rendered, stateName string) (composeVolumes, composeNetworks, retainedExternalVolumes, retainedExternalNetworks []string) {
	for _, definition := range rendered.Volumes {
		if definition.Name == stateName {
			continue
		}
		if definition.External {
			retainedExternalVolumes = append(retainedExternalVolumes, definition.Name)
		} else {
			composeVolumes = append(composeVolumes, definition.Name)
		}
	}
	for _, definition := range rendered.Networks {
		if definition.External {
			retainedExternalNetworks = append(retainedExternalNetworks, definition.Name)
		} else {
			composeNetworks = append(composeNetworks, definition.Name)
		}
	}
	sort.Strings(composeVolumes)
	sort.Strings(composeNetworks)
	sort.Strings(retainedExternalVolumes)
	sort.Strings(retainedExternalNetworks)
	return composeVolumes, composeNetworks, retainedExternalVolumes, retainedExternalNetworks
}
