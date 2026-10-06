package app

import (
	"context"
	"fmt"
	"io"
	"regexp"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/compose"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
	"github.com/infrasecture/hcorral/internal/legacyguard"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

func (r *startupReport) add(out io.Writer, format string, args ...any) {
	line := fmt.Sprintf("hcorral: "+format+"\n", args...)
	r.Text += line
	fmt.Fprint(out, line)
}

func renderedImage(rendered compose.Rendered) (string, error) {
	service, ok := rendered.Services["hcorral"]
	if !ok || service.Image == "" {
		return "", fmt.Errorf("rendered Compose service hcorral has no image")
	}
	return service.Image, nil
}

// prepareStartImage refreshes only the moving default. A failed refresh cannot
// select a different cached image for an existing stopped container.
func prepareStartImage(ctx context.Context, docker containerruntime.Docker, reference string, autoPull, existing bool, streams Streams, report *startupReport) (*containerruntime.Image, error) {
	report.Image = reference
	if !autoPull || !containerruntime.IsLatest(reference) {
		if existing {
			return nil, nil
		}
		if err := ensureSelectedImage(ctx, docker, reference, streams); err != nil {
			return nil, err
		}
		return docker.InspectImage(ctx, reference)
	}
	fmt.Fprintf(streams.Err, "hcorral: checking published image %s\n", reference)
	if err := docker.PullImage(ctx, reference, streams.Out, streams.Err); err != nil {
		if existing {
			report.add(streams.Err, "image refresh failed; starting the existing container with its original image and mounts. Retry `hcorral pull` when the registry is reachable.")
			return nil, nil
		}
		image, inspectErr := docker.InspectImage(ctx, reference)
		if inspectErr != nil {
			return nil, inspectErr
		}
		if image == nil {
			return nil, fmt.Errorf("image refresh failed and no local image is available: %s: %w", reference, err)
		}
		report.add(streams.Err, "image refresh failed; using cached %s. Retry `hcorral pull` when the registry is reachable.", reference)
		return image, nil
	}
	image, err := docker.InspectImage(ctx, reference)
	if err != nil {
		return nil, err
	}
	if image == nil || image.ID == "" {
		return nil, fmt.Errorf("pull completed but selected image is unavailable: %s", reference)
	}
	report.Refreshed = true
	report.add(streams.Err, "refreshed published image %s.", reference)
	return image, nil
}

func stopped(container *containerruntime.Container) bool {
	return container != nil && !container.State.Running && !container.State.Paused && !container.State.Restarting &&
		(container.State.Status == "exited" || container.State.Status == "created")
}

// Call after a long pull and immediately before a mutation. The project lock
// serializes local launchers; it cannot exclude a client on another host.
func recheckProject(ctx context.Context, docker containerruntime.Docker, workspace identity.Workspace, expected *containerruntime.Container) (*containerruntime.Container, []containerruntime.Container, error) {
	containers, err := docker.ListContainers(ctx)
	if err != nil {
		return nil, nil, err
	}
	current := findContainer(containers, workspace.Project)
	if err := identity.VerifyContainer(current, workspace); err != nil {
		return nil, nil, err
	}
	if err := verifyProjectContainers(containers, workspace, current); err != nil {
		return nil, nil, err
	}
	if legacy := legacyguard.Find(containers, workspace.Path); legacy != nil {
		return nil, nil, fmt.Errorf("myCodex environment %s appeared during startup; use the original launcher", legacy.Container)
	}
	if expected == nil && current != nil || expected != nil && (current == nil || current.ID != expected.ID) {
		return nil, nil, fmt.Errorf("container changed during startup; retry to inspect and attach safely")
	}
	if current != nil && !stopped(current) && (!current.State.Running || current.State.Paused || current.State.Restarting) {
		return nil, nil, fmt.Errorf("container state changed to %s; inspect with `hcorral ps` before retrying", stateOf(current))
	}
	return current, containers, nil
}

var composeHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

func reproducibleProject(rendered compose.Rendered, containers []containerruntime.Container, workspace identity.Workspace) bool {
	deployed := map[string]string{}
	for _, container := range containers {
		if container.Config.Labels["com.docker.compose.project"] != workspace.Project {
			continue
		}
		service, hash := container.Config.Labels["com.docker.compose.service"], container.Config.Labels["com.docker.compose.config-hash"]
		if !composeHashPattern.MatchString(hash) || rendered.Hashes[service] != hash {
			return false
		}
		deployed[service] = hash
	}
	drift, _ := compareDrift(rendered, deployed)
	return drift == "none"
}

func startStopped(ctx context.Context, cfg config.Config, workspace identity.Workspace, candidate *containerruntime.Container, streams Streams, runner command.Runner, docker containerruntime.Docker) (startupReport, error) {
	report := startupReport{}
	if !stopped(candidate) {
		return report, fmt.Errorf("container is %s, not stopped; inspect with `hcorral ps`", stateOf(candidate))
	}
	if err := guardExistingGUI(ctx, cfg, workspace, candidate, runner); err != nil {
		return report, err
	}
	if err := validateStateOwnership(ctx, docker, cfg, workspace); err != nil {
		return report, err
	}
	startOriginal := func() (startupReport, error) {
		current, _, err := recheckProject(ctx, docker, workspace, candidate)
		if err != nil {
			return report, err
		}
		if current.State.Running {
			return report, nil
		}
		return report, docker.StartContainer(ctx, current.ID)
	}
	if !cfg.AutoPull {
		return startOriginal()
	}
	// Preserve deployed GUI even when a persistent environment default changed.
	// Explicit CLI requests were checked above; changing mode requires explicit up.
	cfg.GUI = config.GUIIntent{}
	_, _, generated, project, err := prepareProject(ctx, cfg, workspace, candidate, streams, runner)
	if generated.Path != "" {
		defer generated.Cleanup()
	}
	var rendered compose.Rendered
	if err == nil {
		rendered, err = project.Render(ctx)
	}
	if err != nil {
		report.add(streams.Err, "could not reproduce the stopped configuration; keeping its original image and mounts. Repeat its -v/-f options with `hcorral up -d` to update.")
		return startOriginal()
	}
	reference, err := renderedImage(rendered)
	if err != nil {
		return report, err
	}
	image, err := prepareStartImage(ctx, docker, reference, cfg.AutoPull, true, streams, &report)
	if err != nil {
		return report, err
	}
	current, containers, err := recheckProject(ctx, docker, workspace, candidate)
	if err != nil {
		return report, err
	}
	if current.State.Running {
		return report, nil
	}
	if !report.Refreshed || image == nil || image.ID == current.ImageID {
		return startOriginal()
	}
	// Overlays may have changed while the image was downloading. Verify the
	// configuration that Compose would use now, including its selected image.
	rendered, err = project.Render(ctx)
	if err != nil {
		report.add(streams.Err, "configuration became unavailable during refresh; keeping the original container. Use `hcorral up -d` explicitly to update.")
		return startOriginal()
	}
	finalReference, err := renderedImage(rendered)
	if err != nil || finalReference != reference {
		report.add(streams.Err, "selected image changed during refresh; keeping the original container. Retry with the intended configuration.")
		return startOriginal()
	}
	if current.ImageID == "" || !reproducibleProject(rendered, containers, workspace) {
		report.add(streams.Err, "a different image is available, but the stopped configuration could not be reproduced. Keeping its original image and mounts. Repeat its --gui, -v and -f options with `hcorral up -d` to apply the update.")
		return startOriginal()
	}
	if err := validateComposeNetworkOwnership(ctx, docker, rendered, workspace); err != nil {
		return report, err
	}
	if err := validateComposeVolumeOwnership(ctx, docker, rendered, workspace); err != nil {
		return report, err
	}
	if err := validateStateOwnership(ctx, docker, cfg, workspace); err != nil {
		return report, err
	}
	current, containers, err = recheckProject(ctx, docker, workspace, candidate)
	if err != nil {
		return report, err
	}
	if current.State.Running {
		return report, nil
	}
	if !reproducibleProject(rendered, containers, workspace) {
		return report, fmt.Errorf("project configuration changed during image refresh; retry to attach safely")
	}
	report.add(streams.Err, "applying the refreshed image to the stopped container; its home and workspace mounts are preserved.")
	// Sidecars are checked above but are never restarted or recreated implicitly.
	return report, project.Run(ctx, "up", "-d", "--no-build", "--pull", "never", "--no-deps", "hcorral")
}
