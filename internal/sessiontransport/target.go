// Package sessiontransport runs a supplied transfer helper against inspected
// workstation storage. It never prepares Compose configuration or pulls images.
package sessiontransport

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/infrasecture/hcorral/internal/identity"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

type Target struct {
	ContainerID string
	ImageID     string
	UID, GID    string
	Groups      []string
	Home        string
	CodexHome   string
	SQLiteHome  string
	Workdir     string
	Mounts      []containerruntime.Mount
	SQLiteEnv   string
}

// InspectTarget accepts only facts from an existing, ownership-verified corral.
// No identity, home or image is guessed from current launcher defaults.
func InspectTarget(container *containerruntime.Container) (Target, error) {
	return InspectTargetWithSQLite(container, "")
}

// InspectTargetWithSQLite selects a resolved metadata directory. A relocated
// database receives only its required persistent mounts; it cannot silently
// fall back to a database baked into the helper image or container rootfs.
func InspectTargetWithSQLite(container *containerruntime.Container, sqliteHome string) (Target, error) {
	if container == nil {
		return Target{}, errors.New("session transfer requires an existing Codex corral; no workstation or state volume was created")
	}
	if container.Config.Labels[identity.LabelHarnessType] != "codex" || container.Config.Labels[identity.LabelRuntimeSchema] != identity.RuntimeSchemaVersion {
		return Target{}, errors.New("session transfer requires the supported Codex corral runtime contract")
	}
	if container.ID == "" || container.ImageID == "" {
		return Target{}, errors.New("container inspection lacks its actual container or image ID")
	}
	if container.State.Paused || container.State.Restarting || (container.State.Status != "running" && container.State.Status != "exited" && container.State.Status != "created") {
		return Target{}, fmt.Errorf("session transfer cannot use container state %q", container.State.Status)
	}
	env := make(map[string]string)
	for _, assignment := range container.Config.Env {
		key, value, ok := strings.Cut(assignment, "=")
		if !ok {
			continue
		}
		switch key {
		case "HCORRAL_HOST_UID", "HCORRAL_HOST_GID", "HCORRAL_HOST_GROUPS", "HCORRAL_CONTAINER_HOME", "HCORRAL_WORKDIR", "CODEX_HOME", "CODEX_SQLITE_HOME":
		default:
			continue
		}
		if prior, present := env[key]; present && prior != value {
			return Target{}, fmt.Errorf("container environment has conflicting %s values", key)
		}
		env[key] = value
	}
	uid, err := numericID(env["HCORRAL_HOST_UID"])
	if err != nil {
		return Target{}, fmt.Errorf("runtime UID: %w", err)
	}
	gid, err := numericID(env["HCORRAL_HOST_GID"])
	if err != nil {
		return Target{}, fmt.Errorf("runtime GID: %w", err)
	}
	home, workdir := env["HCORRAL_CONTAINER_HOME"], env["HCORRAL_WORKDIR"]
	if !absolutePath(home) || home == "/" || !absolutePath(workdir) {
		return Target{}, errors.New("container inspection lacks a valid runtime home or working directory")
	}
	// Runtime schema 1 sets CODEX_HOME explicitly when invoking Codex. A
	// conflicting Docker-level override is ambiguous, not a new mount to guess.
	codexHome := path.Join(home, ".codex")
	if sqliteHome == "" {
		sqliteHome = codexHome
	}
	if !absolutePath(sqliteHome) || sqliteHome == "/" {
		return Target{}, errors.New("effective SQLite home must be an absolute directory")
	}
	if value := env["CODEX_HOME"]; value != "" && value != codexHome {
		return Target{}, errors.New("Docker CODEX_HOME conflicts with the corral runtime home; resolve the deployed configuration before transfer")
	}
	seen := map[string]bool{gid: true}
	if env["HCORRAL_HOST_GROUPS"] == "" {
		return Target{}, errors.New("container inspection lacks runtime supplementary groups")
	}
	for _, spec := range strings.Split(env["HCORRAL_HOST_GROUPS"], ",") {
		group, _, _ := strings.Cut(spec, ":")
		group, err = numericID(group)
		if err != nil {
			return Target{}, fmt.Errorf("runtime supplementary group: %w", err)
		}
		seen[group] = true
	}
	groups := make([]string, 0, len(seen))
	for group := range seen {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	deployed := append([]containerruntime.Mount(nil), container.Mounts...)
	definitions := make(map[string]containerruntime.MountDefinition)
	for _, definition := range container.HostConfig.Mounts {
		if _, duplicate := definitions[definition.Target]; duplicate {
			return Target{}, errors.New("duplicate deployed mount definitions")
		}
		definitions[definition.Target] = definition
	}
	for i := range deployed {
		definition, present := definitions[deployed[i].Destination]
		if !present || definition.Type != "volume" {
			continue
		}
		if deployed[i].Type != "volume" || (definition.Source != "" && definition.Source != deployed[i].Name) {
			return Target{}, errors.New("deployed volume definition disagrees with the actual storage mount")
		}
		deployed[i].Subpath = definition.VolumeOptions.Subpath
	}
	mounts, err := storageMounts(deployed, codexHome, true)
	if err != nil {
		return Target{}, err
	}
	databaseMounts, err := storageMounts(deployed, sqliteHome, false)
	if err != nil {
		return Target{}, fmt.Errorf("effective SQLite storage: %w", err)
	}
	for _, mount := range databaseMounts {
		alreadySelected := false
		for _, selected := range mounts {
			if selected.Destination == mount.Destination {
				alreadySelected = true
				break
			}
		}
		if alreadySelected {
			continue
		}
		// Narrow a separate metadata mount to the selected directory. A
		// database under the workspace must not expose the whole workspace.
		if contains(mount.Destination, sqliteHome) && mount.Destination != sqliteHome {
			relative := strings.TrimPrefix(strings.TrimPrefix(sqliteHome, mount.Destination), "/")
			if mount.Type == "bind" {
				mount.Source = path.Join(mount.Source, relative)
			} else {
				mount.Subpath = path.Join(mount.Subpath, relative)
			}
			mount.Destination = sqliteHome
		}
		mount.RW = false
		mounts = append(mounts, mount)
	}
	sort.Slice(mounts, func(i, j int) bool { return mounts[i].Destination < mounts[j].Destination })
	return Target{ContainerID: container.ID, ImageID: container.ImageID, UID: uid, GID: gid, Groups: groups, Home: home, CodexHome: codexHome, SQLiteHome: sqliteHome, Workdir: workdir, Mounts: mounts, SQLiteEnv: env["CODEX_SQLITE_HOME"]}, nil
}

func absolutePath(value string) bool {
	return path.IsAbs(value) && path.Clean(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}

func numericID(value string) (string, error) {
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return "", errors.New("expected an unsigned numeric ID")
		}
	}
	n, err := strconv.ParseUint(value, 10, 32)
	if err != nil || n == 1<<32-1 {
		return "", fmt.Errorf("invalid numeric ID %q", value)
	}
	return strconv.FormatUint(n, 10), nil
}

func contains(root, child string) bool {
	return root == child || strings.HasPrefix(child, strings.TrimSuffix(root, "/")+"/")
}

// Reproduce the mount covering the selected home and any mounts below it.
// Workspace and GUI mounts outside that storage are deliberately not attached.
func storageMounts(all []containerruntime.Mount, home string, writable bool) ([]containerruntime.Mount, error) {
	var base *containerruntime.Mount
	seen := make(map[string]bool)
	for _, mount := range all {
		if !absolutePath(mount.Destination) || seen[mount.Destination] {
			return nil, errors.New("ambiguous or invalid deployed mount targets")
		}
		seen[mount.Destination] = true
		if contains(mount.Destination, home) && (base == nil || len(mount.Destination) > len(base.Destination)) {
			copy := mount
			base = &copy
		}
	}
	if base == nil {
		return nil, errors.New("Codex home is not in persistent mounted storage")
	}
	if writable && !base.RW {
		return nil, errors.New("Codex home mount is read-only; transfer needs its writable writer-lock namespace, including for export")
	}
	selected := []containerruntime.Mount{*base}
	for _, mount := range all {
		if mount.Destination != base.Destination && contains(home, mount.Destination) {
			// SQLite uses direct state files and their sidecars. Nested
			// workspace/GUI mounts below a metadata root are unrelated.
			if !writable && (path.Dir(mount.Destination) != home || !databaseFile(path.Base(mount.Destination))) {
				continue
			}
			selected = append(selected, mount)
		}
	}
	for _, mount := range selected {
		switch mount.Type {
		case "volume":
			if mount.Name == "" || strings.ContainsAny(mount.Name, "/\x00\r\n") {
				return nil, errors.New("deployed state volume has no usable name")
			}
			if value := mount.Subpath; value != "" && (path.IsAbs(value) || path.Clean(value) != value || value == ".." || strings.HasPrefix(value, "../") || strings.ContainsAny(value, "\x00\r\n")) {
				return nil, errors.New("deployed state volume has an invalid subpath")
			}
		case "bind":
			if !absolutePath(mount.Source) {
				return nil, errors.New("deployed state bind has no absolute daemon-side source")
			}
		default:
			return nil, fmt.Errorf("unsupported Codex storage mount type %q at %s", mount.Type, mount.Destination)
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Destination < selected[j].Destination })
	return selected, nil
}

func databaseFile(name string) bool {
	if !strings.HasPrefix(name, "state_") {
		return false
	}
	for _, suffix := range []string{".sqlite", ".sqlite-wal", ".sqlite-shm", ".sqlite-journal"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}
