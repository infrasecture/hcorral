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
		if reason := legacyMarker(container); reason != "" {
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

func legacyMarker(container containerruntime.Container) string {
	labels := container.Config.Labels
	if _, ok := labels["io.infrasecture.mycodex.gui"]; ok {
		return "myCodex GUI label and same-path workspace bind"
	}
	if labels["com.docker.compose.service"] == "codex" && strings.HasSuffix(container.CleanName(), "-codex") {
		return "myCodex Compose service/name and same-path workspace bind"
	}
	if strings.HasPrefix(container.Config.Image, "ghcr.io/infrasecture/harness-workstation:") && strings.HasSuffix(container.CleanName(), "-codex") {
		return "myCodex image/name and same-path workspace bind"
	}
	return ""
}
