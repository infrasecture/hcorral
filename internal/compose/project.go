package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/infrasecture/hcorral/internal/command"
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
	Networks map[string]RenderedTopLevelNetwork
	Hashes   map[string]string
}

type RenderedTopLevelVolume struct {
	Name     string `json:"name"`
	External bool   `json:"external"`
}

type RenderedTopLevelNetwork struct {
	Name     string `json:"name"`
	External bool   `json:"external"`
}

type RenderedService struct {
	Image string `json:"image"`
}

func (p Project) Capture(ctx context.Context, args ...string) (command.Result, error) {
	argv := p.Invocation.Args(args...)
	result, err := p.Runner.Capture(ctx, argv, p.Invocation.Env)
	if err != nil {
		// Compose diagnostics may contain interpolated secrets from user overlays.
		// Keep captured output available to the caller for deliberate handling, but
		// never promote it into a launcher error or structured information output.
		return result, fmt.Errorf("Compose %s failed: %w", first(args), err)
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

// Render performs no policy validation. The embedded base is first and later
// files are trusted Compose overlays; their final rendered result is
// diagnostic truth, including when they replace the image, mounts, labels, or
// services.
func (p Project) Render(ctx context.Context) (Rendered, error) {
	result, err := p.Capture(ctx, "config", "--format", "json")
	if err != nil {
		return Rendered{}, err
	}
	var document struct {
		Services map[string]RenderedService         `json:"services"`
		Volumes  map[string]RenderedTopLevelVolume  `json:"volumes"`
		Networks map[string]RenderedTopLevelNetwork `json:"networks"`
	}
	if err := json.Unmarshal(result.Stdout, &document); err != nil {
		return Rendered{}, fmt.Errorf("decode rendered Compose project: %w", err)
	}
	return Rendered{Services: document.Services, Volumes: document.Volumes, Networks: document.Networks, Hashes: p.hashes(ctx)}, nil
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

func first(values []string) string {
	if len(values) == 0 {
		return "command"
	}
	return values[0]
}
