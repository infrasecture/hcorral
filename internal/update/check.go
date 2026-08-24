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

const CodexLabel = "ai.infrasecture.hcorral.codex.version"

type Facts struct {
	Enabled         bool   `json:"enabled"`
	Pinned          bool   `json:"pinned"`
	Current         string `json:"current"`
	Selected        string `json:"selected"`
	Latest          string `json:"latest"`
	SelectedNewer   bool   `json:"selected_newer"`
	UpstreamNewer   bool   `json:"upstream_newer"`
	LookupStatus    string `json:"lookup_status"`
	LookupErrorKind string `json:"lookup_error_kind"`
}

type Checker struct {
	Docker      containerruntime.Docker
	Client      *http.Client
	RegistryURL string
	Out         io.Writer
}

func (c Checker) Notify(ctx context.Context, cfg config.Config, container *containerruntime.Container) {
	if !cfg.UpdateCheck || container == nil || !container.State.Running {
		return
	}
	facts := c.Inspect(ctx, cfg, container)
	current, err := Parse(facts.Current)
	if err != nil {
		return
	}
	if selected, parseErr := Parse(facts.Selected); parseErr == nil && Compare(current, selected) < 0 {
		fmt.Fprintf(c.Out, "hcorral: selected image %s:%s contains Codex %s; this container runs %s. Run `hcorral up -d` to apply it.\n", cfg.ImageName, cfg.ImageTag, selected.Raw, current.Raw)
	}
	latest, err := Parse(facts.Latest)
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

func (c Checker) Inspect(ctx context.Context, cfg config.Config, container *containerruntime.Container) Facts {
	facts := Facts{Enabled: cfg.UpdateCheck, Pinned: cfg.ImageTag != "latest", LookupStatus: "disabled"}
	if container != nil {
		facts.Current = c.imageVersion(ctx, container.Config.Image)
		if _, err := Parse(facts.Current); err != nil && container.State.Running {
			if result, execErr := c.Docker.ExecCapture(ctx, container.CleanName(), "codex", "--version"); execErr == nil {
				facts.Current = extractVersion(string(result.Stdout))
			}
		}
	}
	facts.Selected = c.imageVersion(ctx, cfg.ImageName+":"+cfg.ImageTag)
	current, currentErr := Parse(facts.Current)
	if selected, err := Parse(facts.Selected); err == nil && currentErr == nil {
		facts.SelectedNewer = Compare(current, selected) < 0
	}
	if !cfg.UpdateCheck {
		return facts
	}
	facts.LookupStatus = "unavailable"
	latest, err := c.latest(ctx)
	if err != nil {
		facts.LookupErrorKind = classifyLookupError(err)
		return facts
	}
	facts.Latest = latest
	parsed, parseErr := Parse(latest)
	if parseErr != nil {
		facts.Latest = ""
		facts.LookupErrorKind = "invalid-response"
		return facts
	}
	facts.LookupStatus = "ok"
	if currentErr == nil {
		facts.UpstreamNewer = Compare(current, parsed) < 0
	}
	return facts
}

func (c Checker) imageVersion(ctx context.Context, reference string) string {
	image, err := c.Docker.InspectImage(ctx, reference)
	if err != nil || image == nil {
		return ""
	}
	return image.Config.Labels[CodexLabel]
}

func (c Checker) latest(ctx context.Context) (string, error) {
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	registryURL := c.RegistryURL
	if registryURL == "" {
		registryURL = "https://registry.npmjs.org/@openai%2Fcodex/latest"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, registryURL, nil)
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

func classifyLookupError(err error) string {
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "deadline"), strings.Contains(text, "timeout"):
		return "timeout"
	case strings.Contains(text, "decode"), strings.Contains(text, "semver"):
		return "invalid-response"
	case strings.Contains(text, "registry returned"):
		return "http"
	default:
		return "network"
	}
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
