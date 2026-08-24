package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/infrasecture/hcorral/internal/config"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

const codexLabel = "ai.infrasecture.hcorral.codex.version"

type Checker struct {
	Docker containerruntime.Docker
	Client *http.Client
	Out    io.Writer
}

func (c Checker) Notify(ctx context.Context, cfg config.Config, container *containerruntime.Container) {
	if !cfg.UpdateCheck || container == nil || !container.State.Running {
		return
	}
	currentRaw := c.imageVersion(ctx, container.Config.Image)
	if _, err := Parse(currentRaw); err != nil {
		if result, execErr := c.Docker.ExecCapture(ctx, container.CleanName(), "codex", "--version"); execErr == nil {
			currentRaw = extractVersion(string(result.Stdout))
		}
	}
	current, err := Parse(currentRaw)
	if err != nil {
		return
	}
	selectedRaw := c.imageVersion(ctx, cfg.ImageName+":"+cfg.ImageTag)
	if selected, parseErr := Parse(selectedRaw); parseErr == nil && Compare(current, selected) < 0 {
		fmt.Fprintf(c.Out, "hcorral: selected image %s:%s contains Codex %s; this container runs %s. Run `hcorral up -d` to apply it.\n", cfg.ImageName, cfg.ImageTag, selected.Raw, current.Raw)
	}
	latestRaw, err := c.latest(ctx)
	if err != nil {
		return
	}
	latest, err := Parse(latestRaw)
	if err != nil || Compare(current, latest) >= 0 {
		return
	}
	fmt.Fprintf(c.Out, "hcorral: Codex %s is available upstream; this container runs %s.\n", latest.Raw, current.Raw)
	if cfg.ImageTag == "latest" {
		fmt.Fprintln(c.Out, "hcorral: run `hcorral pull` to refresh the selected image; pulling does not recreate the container.")
	} else {
		fmt.Fprintf(c.Out, "hcorral: HCORRAL_IMAGE_TAG=%s pins the selected image; select a newer tag to update.\n", cfg.ImageTag)
	}
}

func (c Checker) imageVersion(ctx context.Context, reference string) string {
	image, err := c.Docker.InspectImage(ctx, reference)
	if err != nil || image == nil {
		return ""
	}
	return image.Config.Labels[codexLabel]
}

func (c Checker) latest(ctx context.Context) (string, error) {
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://registry.npmjs.org/@openai%2Fcodex/latest", nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("npm registry returned %s", response.Status)
	}
	var document struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document); err != nil {
		return "", err
	}
	return document.Version, nil
}

func extractVersion(output string) string {
	for _, field := range strings.Fields(output) {
		trimmed := strings.TrimPrefix(field, "v")
		if _, err := Parse(trimmed); err == nil {
			return trimmed
		}
	}
	return ""
}
