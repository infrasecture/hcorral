package compose

import (
	"os"
	"strconv"
	"strings"

	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
)

type Invocation struct {
	Prefix    []string
	Files     []string
	Project   string
	Directory string
	Env       []string
}

func NewInvocation(cfg config.Config, workspace identity.Workspace, paths AssetPaths, guiFile string, guiEnv map[string]string) (Invocation, error) {
	stateVolume := cfg.StateVolumeName
	if stateVolume == "" {
		if cfg.StateMode == config.StatePrivate {
			stateVolume = identity.WorkspaceVolumeName(workspace)
		} else {
			stateVolume = "hcorral_state"
		}
	}
	current, err := identity.CurrentHost()
	if err != nil {
		return Invocation{}, err
	}

	env := map[string]string{
		"HCORRAL_LAUNCHED_BY_WRAPPER": "1",
		"HCORRAL_WORKSPACE":           cfg.Workspace,
		"HCORRAL_WORKSPACE_ID":        workspace.FullID,
		"HCORRAL_CORRAL_ID":           workspace.CorralID,
		"HCORRAL_HARNESS_TYPE":        workspace.Harness,
		"HCORRAL_CONTAINER_NAME":      workspace.Project,
		"HCORRAL_IMAGE":               cfg.Image,
		"HCORRAL_STATE_VOLUME_NAME":   stateVolume,
		"HCORRAL_HOST_UID":            current.UID,
		"HCORRAL_HOST_GID":            current.GID,
		"HCORRAL_HOST_USER":           current.User,
		"HCORRAL_HOST_GROUP":          current.Group,
		"HCORRAL_HOST_GROUPS":         strings.Join(current.Groups, ","),
		"HCORRAL_CONTAINER_HOME":      cfg.ContainerHome,
		"HCORRAL_WORKDIR":             cfg.Workdir,
		"HCORRAL_BYOBU_SESSION":       cfg.Session,
		"HCORRAL_AUTO_ATTACH":         strconv.FormatBool(cfg.AutoAttach),
		"HCORRAL_ATTACH_HINT":         "hcorral",
		"HCORRAL_GUI_MODE":            "none",
	}
	for key, value := range guiEnv {
		env[key] = value
	}

	files := []string{paths.Base}
	if guiFile != "" {
		files = append(files, guiFile)
	}
	files = append(files, cfg.ComposeFiles...)
	return Invocation{
		Prefix:    append([]string(nil), cfg.ComposeCommand...),
		Files:     files,
		Project:   workspace.Project,
		Directory: workspace.Path,
		Env:       mergeEnvironment(os.Environ(), env),
	}, nil
}

func (i Invocation) Args(composeArgs ...string) []string {
	argv := append([]string(nil), i.Prefix...)
	argv = append(argv, "-p", i.Project, "--project-directory", i.Directory)
	for _, file := range i.Files {
		argv = append(argv, "-f", file)
	}
	return append(argv, composeArgs...)
}

func mergeEnvironment(environ []string, managed map[string]string) []string {
	result := make([]string, 0, len(environ)+len(managed))
	for _, entry := range environ {
		key := entry
		if index := strings.IndexByte(entry, '='); index >= 0 {
			key = entry[:index]
		}
		if strings.HasPrefix(key, "COMPOSE_") {
			continue
		}
		if _, replaced := managed[key]; replaced {
			continue
		}
		result = append(result, entry)
	}
	for key, value := range managed {
		result = append(result, key+"="+value)
	}
	return result
}
