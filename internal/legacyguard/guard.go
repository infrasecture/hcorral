package legacyguard

import (
	"strings"

	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

type Match struct {
	Container string `json:"container"`
	State     string `json:"state"`
	Reason    string `json:"reason"`
}

func Find(containers []containerruntime.Container, workspace string) *Match {
	for _, container := range containers {
		if !hasWorkspaceBind(container, workspace) {
			continue
		}
		if reason, ambiguous := legacyEvidence(container); reason != "" {
			if ambiguous {
				reason = "ambiguous myCodex evidence: " + reason
			}
			return &Match{Container: container.CleanName(), State: container.State.Status, Reason: reason}
		}
	}
	return nil
}

func hasWorkspaceBind(container containerruntime.Container, workspace string) bool {
	for _, mount := range container.Mounts {
		if mount.Type == "bind" && mount.Source == workspace && mount.Destination == workspace {
			return true
		}
	}
	return false
}

func legacyEvidence(container containerruntime.Container) (reason string, ambiguous bool) {
	labels := container.Config.Labels
	for key := range labels {
		if strings.HasPrefix(key, "io.infrasecture.mycodex.") {
			return "myCodex label and same-path workspace bind", false
		}
	}

	serviceMarker := labels["com.docker.compose.service"] == "codex"
	nameMarker := strings.HasSuffix(container.CleanName(), "-codex")
	imageMarker := strings.HasPrefix(container.Config.Image, "ghcr.io/infrasecture/harness-workstation:")
	if serviceMarker && nameMarker {
		return "myCodex Compose service/name and same-path workspace bind", false
	}
	if imageMarker && nameMarker {
		return "myCodex image/name and same-path workspace bind", false
	}

	markers := make([]string, 0, 3)
	if serviceMarker {
		markers = append(markers, "Compose service codex")
	}
	if nameMarker {
		markers = append(markers, "container name ending in -codex")
	}
	if imageMarker {
		markers = append(markers, "legacy workstation image")
	}
	if len(markers) > 0 {
		return strings.Join(markers, ", ") + " with same-path workspace bind", true
	}
	return "", false
}
