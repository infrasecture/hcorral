package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/harness"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

const (
	HarnessVersionLabel = "ai.infrasecture.hcorral.harness.version"
	CodexLabel          = "ai.infrasecture.hcorral.codex.version"
)

type Facts struct {
	LauncherCurrent string `json:"launcher_current"`
	LauncherLatest  string `json:"launcher_latest"`
	LauncherNewer   bool   `json:"launcher_newer"`
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
	Docker          containerruntime.Docker
	Client          *http.Client
	RegistryURL     string
	Out             io.Writer
	LauncherVersion string
}

func (c Checker) Notify(ctx context.Context, cfg config.Config, container *containerruntime.Container) {
	if !cfg.UpdateCheck || container == nil || !container.State.Running {
		return
	}
	facts := c.Inspect(ctx, cfg, container)
	if facts.LauncherNewer {
		fmt.Fprintf(c.Out, "hcorral: launcher %s is available; this binary is %s.\n", facts.LauncherLatest, facts.LauncherCurrent)
	}
	current, err := Parse(facts.Current)
	if err != nil {
		return
	}
	if selected, parseErr := Parse(facts.Selected); parseErr == nil && Compare(current, selected) < 0 {
		fmt.Fprintf(c.Out, "hcorral: selected image %s contains %s %s; this container runs %s. Run `hcorral up -d` to apply it.\n", cfg.Image, cfg.Harness, selected.Raw, current.Raw)
	}
	latest, err := Parse(facts.Latest)
	if err != nil || Compare(current, latest) >= 0 {
		return
	}
	fmt.Fprintf(c.Out, "hcorral: %s %s is available upstream; this container runs %s.\n", cfg.Harness, latest.Raw, current.Raw)
	if !imagePinned(cfg.Image) {
		fmt.Fprintln(c.Out, "hcorral: run `hcorral pull` to refresh the selected image; pulling does not recreate the container.")
	} else {
		fmt.Fprintf(c.Out, "hcorral: selected image %s is pinned; select a newer reference to update.\n", cfg.Image)
	}
}

func (c Checker) Inspect(ctx context.Context, cfg config.Config, container *containerruntime.Container) Facts {
	facts := Facts{Enabled: cfg.UpdateCheck, Pinned: imagePinned(cfg.Image), LookupStatus: "disabled", LauncherCurrent: c.LauncherVersion}
	if container != nil {
		facts.Current = c.imageVersion(ctx, container.Config.Image, cfg.Harness)
		if container.State.Running {
			command := cfg.Harness
			if definition, ok := harness.Lookup(cfg.Harness); ok {
				command = definition.Command
			}
			uid := containerEnvironment(container, "HCORRAL_HOST_UID")
			probe := []string{"gosu", uid, "env", "HOME=" + cfg.ContainerHome, "bash", "--login", "-c", `exec "$@"`, "bash", command, "--version"}
			if uid != "" {
				if result, execErr := c.Docker.ExecCapture(ctx, container.CleanName(), probe...); execErr == nil {
					if installed := extractVersion(string(result.Stdout)); installed != "" {
						facts.Current = installed
					}
				}
			}
		}
	}
	facts.Selected = c.imageVersion(ctx, cfg.Image, cfg.Harness)
	current, currentErr := Parse(facts.Current)
	if selected, err := Parse(facts.Selected); err == nil && currentErr == nil {
		facts.SelectedNewer = Compare(current, selected) < 0
	}
	if !cfg.UpdateCheck {
		return facts
	}
	if latest, err := c.latestLauncher(ctx); err == nil {
		facts.LauncherLatest = latest
		current := strings.TrimPrefix(c.LauncherVersion, "v")
		if currentVersion, currentErr := Parse(current); currentErr == nil {
			if latestVersion, latestErr := Parse(strings.TrimPrefix(latest, "v")); latestErr == nil {
				facts.LauncherNewer = Compare(currentVersion, latestVersion) < 0
			}
		}
	}
	facts.LookupStatus = "unavailable"
	latest, err := c.latest(ctx, cfg.Harness)
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

func (c Checker) latestLauncher(ctx context.Context) (string, error) {
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/infrasecture/hcorral/releases/latest", nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub releases returned %s", response.Status)
	}
	var document struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document); err != nil {
		return "", err
	}
	if document.TagName == "" {
		return "", errors.New("GitHub release tag is empty")
	}
	return document.TagName, nil
}

func (c Checker) imageVersion(ctx context.Context, reference, harnessType string) string {
	image, err := c.Docker.InspectImage(ctx, reference)
	if err != nil || image == nil {
		return ""
	}
	if version := image.Config.Labels[HarnessVersionLabel]; version != "" {
		return version
	}
	label := "ai.infrasecture.hcorral." + harnessType + ".version"
	if definition, ok := harness.Lookup(harnessType); ok {
		label = definition.VersionLabel
	}
	return image.Config.Labels[label]
}

func (c Checker) latest(ctx context.Context, harnessType string) (string, error) {
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	registryURL := c.RegistryURL
	responseKind := "npm"
	if registryURL == "" {
		switch harnessType {
		case "codex":
			registryURL = "https://api.github.com/repos/openai/codex/releases/latest"
			responseKind = "codex-github"
		case "claude":
			registryURL = "https://downloads.claude.ai/claude-code-releases/latest"
			responseKind = "plain"
		case "pi":
			registryURL = "https://registry.npmjs.org/@earendil-works%2Fpi-coding-agent/latest"
		default:
			return "", fmt.Errorf("no update source for custom harness %s", harnessType)
		}
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
		return "", fmt.Errorf("upstream version source returned %s", response.Status)
	}
	if responseKind == "plain" {
		content, readErr := io.ReadAll(io.LimitReader(response.Body, 256))
		if readErr != nil {
			return "", readErr
		}
		return strings.TrimSpace(string(content)), nil
	}
	var document struct {
		Version string `json:"version"`
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&document); err != nil {
		return "", err
	}
	if responseKind == "codex-github" {
		return strings.TrimPrefix(document.TagName, "rust-v"), nil
	}
	return document.Version, nil
}

func imagePinned(reference string) bool { return !strings.HasSuffix(reference, ":latest") }

func classifyLookupError(err error) string {
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "deadline"), strings.Contains(text, "timeout"):
		return "timeout"
	case strings.Contains(text, "decode"), strings.Contains(text, "semver"):
		return "invalid-response"
	case strings.Contains(text, "version source returned"):
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

func containerEnvironment(container *containerruntime.Container, key string) string {
	prefix := key + "="
	for _, entry := range container.Config.Env {
		if strings.HasPrefix(entry, prefix) {
			return strings.TrimPrefix(entry, prefix)
		}
	}
	return ""
}
