package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
)

type Project struct {
	Runner     command.Runner
	Invocation Invocation
	Out        io.Writer
	Err        io.Writer
}

type Rendered struct {
	Services map[string]RenderedService
	Volumes  map[string]RenderedTopLevelVolume
	Hashes   map[string]string
}

type RenderedTopLevelVolume struct {
	Name     string `json:"name"`
	External bool   `json:"external"`
}

type RenderedService struct {
	Image         string            `json:"image"`
	ContainerName string            `json:"container_name"`
	WorkingDir    string            `json:"working_dir"`
	Labels        map[string]string `json:"labels"`
	Environment   map[string]string `json:"environment"`
	Volumes       []RenderedVolume  `json:"volumes"`
}

type RenderedVolume struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

func (p Project) Capture(ctx context.Context, args ...string) (command.Result, error) {
	argv := p.Invocation.Args(args...)
	result, err := p.Runner.Capture(ctx, argv, p.Invocation.Env)
	if err != nil {
		return result, fmt.Errorf("Compose %s failed: %w: %s", first(args), err, strings.TrimSpace(string(result.Stderr)))
	}
	return result, nil
}

func (p Project) Run(ctx context.Context, args ...string) error {
	argv := p.Invocation.Args(args...)
	if err := p.Runner.Run(ctx, argv, p.Invocation.Env, nil, p.Out, p.Err); err != nil {
		return fmt.Errorf("Compose %s failed: %w", first(args), err)
	}
	return nil
}

func (p Project) Replace(args ...string) error {
	return p.Runner.Replace(p.Invocation.Args(args...), p.Invocation.Env)
}

func (p Project) RenderAndValidate(ctx context.Context, cfg config.Config, workspace identity.Workspace, guiMode, stateVolume string) (Rendered, error) {
	result, err := p.Capture(ctx, "config", "--format", "json")
	if err != nil {
		return Rendered{}, err
	}
	var document struct {
		Services map[string]RenderedService        `json:"services"`
		Volumes  map[string]RenderedTopLevelVolume `json:"volumes"`
	}
	if err := json.Unmarshal(result.Stdout, &document); err != nil {
		return Rendered{}, fmt.Errorf("decode rendered Compose project: %w", err)
	}
	service, ok := document.Services["hcorral"]
	if !ok {
		return Rendered{}, fmt.Errorf("rendered Compose project removed required hcorral service")
	}
	wants := map[string]string{
		"image":          cfg.ImageName + ":" + cfg.ImageTag,
		"container_name": workspace.Project,
		"working_dir":    cfg.Workdir,
	}
	if service.Image != wants["image"] {
		return Rendered{}, fmt.Errorf("overlay changed managed image: got %q want %q", service.Image, wants["image"])
	}
	if service.ContainerName != wants["container_name"] {
		return Rendered{}, fmt.Errorf("overlay changed managed container name: got %q want %q", service.ContainerName, wants["container_name"])
	}
	if service.WorkingDir != wants["working_dir"] {
		return Rendered{}, fmt.Errorf("overlay changed managed working directory: got %q want %q", service.WorkingDir, wants["working_dir"])
	}
	for label, want := range map[string]string{
		identity.LabelWorkspaceID:     workspace.FullID,
		identity.LabelWorkspaceScheme: identity.WorkspaceSchemeVersion,
		identity.LabelRuntimeSchema:   identity.RuntimeSchemaVersion,
		identity.LabelGUI:             guiMode,
	} {
		if service.Labels[label] != want {
			return Rendered{}, fmt.Errorf("overlay changed managed label %s: got %q want %q", label, service.Labels[label], want)
		}
	}
	if service.Environment["HCORRAL_LAUNCHED_BY_WRAPPER"] != "1" {
		return Rendered{}, fmt.Errorf("overlay removed launcher guard")
	}
	if !hasVolume(service.Volumes, "bind", cfg.Workspace, cfg.Workspace) {
		return Rendered{}, fmt.Errorf("overlay changed managed workspace mount; rendered volumes: %#v", service.Volumes)
	}
	if !hasVolume(service.Volumes, "volume", "hcorral_state", cfg.ContainerHome) {
		return Rendered{}, fmt.Errorf("overlay changed managed state mount; rendered volumes: %#v", service.Volumes)
	}
	stateDefinition, ok := document.Volumes["hcorral_state"]
	if !ok || !stateDefinition.External || stateDefinition.Name != stateVolume {
		return Rendered{}, fmt.Errorf("overlay changed managed state volume: got %#v, want external name %q", stateDefinition, stateVolume)
	}
	return Rendered{Services: document.Services, Volumes: document.Volumes, Hashes: p.hashes(ctx)}, nil
}

func (p Project) hashes(ctx context.Context) map[string]string {
	result, err := p.Capture(ctx, "config", "--hash", "*")
	if err != nil {
		return nil
	}
	hashes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(result.Stdout)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			hashes[fields[0]] = fields[1]
		}
	}
	return hashes
}

func hasVolume(volumes []RenderedVolume, kind, source, target string) bool {
	for _, volume := range volumes {
		if volume.Type == kind && volume.Source == source && volume.Target == target {
			return true
		}
	}
	return false
}

func first(values []string) string {
	if len(values) == 0 {
		return "command"
	}
	return values[0]
}
