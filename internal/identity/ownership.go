package identity

import (
	"fmt"

	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

const (
	LabelWorkspaceID       = "ai.infrasecture.hcorral.workspace-id"
	LabelWorkspaceScheme   = "ai.infrasecture.hcorral.workspace-id-scheme"
	LabelGUI               = "ai.infrasecture.hcorral.gui"
	LabelRuntimeSchema     = "ai.infrasecture.hcorral.runtime-schema"
	LabelStateKind         = "ai.infrasecture.hcorral.state-kind"
	WorkspaceSchemeVersion = "v1"
	RuntimeSchemaVersion   = "1"
)

func VerifyContainer(container *containerruntime.Container, workspace Workspace) error {
	if container == nil {
		return nil
	}
	labels := container.Config.Labels
	if labels[LabelWorkspaceID] != workspace.FullID {
		return fmt.Errorf("container %s belongs to workspace ID %q, selected workspace ID is %q", container.CleanName(), labels[LabelWorkspaceID], workspace.FullID)
	}
	if labels[LabelWorkspaceScheme] != WorkspaceSchemeVersion {
		return fmt.Errorf("container %s has unsupported workspace identity scheme %q", container.CleanName(), labels[LabelWorkspaceScheme])
	}
	if labels[LabelRuntimeSchema] != RuntimeSchemaVersion {
		return fmt.Errorf("container %s has unsupported runtime schema %q", container.CleanName(), labels[LabelRuntimeSchema])
	}
	if labels["com.docker.compose.project"] != workspace.Project || labels["com.docker.compose.service"] != "hcorral" {
		return fmt.Errorf("container %s lacks exact Compose ownership labels for project %s service hcorral", container.CleanName(), workspace.Project)
	}
	return nil
}

func PrivateVolumeLabels(workspace Workspace) map[string]string {
	return map[string]string{
		LabelWorkspaceID:     workspace.FullID,
		LabelWorkspaceScheme: WorkspaceSchemeVersion,
		LabelRuntimeSchema:   RuntimeSchemaVersion,
		LabelStateKind:       "private",
	}
}

func SharedVolumeLabels() map[string]string {
	return map[string]string{LabelRuntimeSchema: RuntimeSchemaVersion, LabelStateKind: "shared"}
}
