package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/compose"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
	"github.com/infrasecture/hcorral/internal/legacyguard"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
	"github.com/infrasecture/hcorral/internal/update"
)

type snapshot struct {
	Schema        int                `json:"schema"`
	Launcher      snapshotLauncher   `json:"launcher"`
	Workspace     identity.Workspace `json:"workspace"`
	Project       snapshotProject    `json:"project"`
	Configuration snapshotConfig     `json:"configuration"`
	Ownership     snapshotOwnership  `json:"ownership"`
	MyCodex       snapshotMyCodex    `json:"mycodex"`
	Container     snapshotContainer  `json:"container"`
	Image         snapshotImage      `json:"image"`
	State         snapshotState      `json:"state"`
	Mounts        []snapshotMount    `json:"mounts"`
	GUI           snapshotGUI        `json:"gui"`
	Compose       snapshotCompose    `json:"compose"`
	Session       snapshotSession    `json:"session"`
	Update        update.Facts       `json:"update"`
	Docker        snapshotDocker     `json:"docker"`
}

type snapshotLauncher struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Schema  string `json:"runtime_schema"`
}

type snapshotProject struct {
	Name      string                 `json:"name"`
	Service   string                 `json:"service"`
	Container string                 `json:"container"`
	Others    []snapshotOtherProject `json:"other_workspace_projects"`
}

type snapshotOtherProject struct {
	Name     string `json:"name"`
	Harness  string `json:"harness"`
	CorralID string `json:"corral_id"`
	Status   string `json:"status"`
}

type snapshotConfig struct {
	Harness          string            `json:"harness"`
	Image            string            `json:"image"`
	UserConfigFile   string            `json:"user_config_file"`
	Warnings         []string          `json:"warnings"`
	StateMode        config.StateMode  `json:"state_mode"`
	StateVolume      string            `json:"state_volume"`
	GUI              config.GUIIntent  `json:"gui"`
	ComposeCommand   map[string]any    `json:"compose_command"`
	ComposeFiles     []string          `json:"compose_files"`
	ExtraVolumes     []string          `json:"extra_volumes"`
	ContainerHome    string            `json:"container_home"`
	Workdir          string            `json:"workdir"`
	UpdateCheck      bool              `json:"update_check"`
	WaitTimeout      int               `json:"wait_timeout_seconds"`
	ProgressInterval int               `json:"startup_progress_interval_seconds"`
	Session          string            `json:"session"`
	AutoAttach       bool              `json:"auto_attach"`
	Sources          map[string]string `json:"sources"`
}

type snapshotOwnership struct {
	Status          string `json:"status"`
	WorkspaceID     string `json:"workspace_id"`
	WorkspaceScheme string `json:"workspace_id_scheme"`
	RuntimeSchema   string `json:"runtime_schema"`
	Error           string `json:"error"`
}

type snapshotMyCodex struct {
	Status   string             `json:"status"`
	Conflict *legacyguard.Match `json:"conflict"`
}

type snapshotContainer struct {
	Status    string `json:"status"`
	ID        string `json:"id"`
	StartedAt string `json:"started_at"`
	Image     string `json:"image"`
}

type snapshotImage struct {
	SelectedReference string   `json:"selected_reference"`
	SelectedID        string   `json:"selected_id"`
	SelectedDigests   []string `json:"selected_digests"`
	RenderedReference string   `json:"rendered_reference"`
	DeployedReference string   `json:"deployed_reference"`
	DeployedID        string   `json:"deployed_id"`
	DeployedDigests   []string `json:"deployed_digests"`
}

type snapshotState struct {
	Mode    config.StateMode `json:"mode"`
	Volume  string           `json:"volume"`
	Managed bool             `json:"managed"`
	Exists  *bool            `json:"exists"`
	Removal snapshotRemoval  `json:"removal"`
}

type snapshotRemoval struct {
	Action                   string   `json:"action"`
	Target                   string   `json:"target"`
	Reason                   string   `json:"reason"`
	ComposeProject           string   `json:"compose_project"`
	ComposeVolumes           []string `json:"compose_volumes"`
	ComposeNetworks          []string `json:"compose_networks"`
	PostComposeVolumes       []string `json:"post_compose_volumes"`
	RetainedExternalVolumes  []string `json:"retained_external_volumes"`
	RetainedExternalNetworks []string `json:"retained_external_networks"`
	PreflightRefusalReasons  []string `json:"preflight_refusal_reasons"`
}

type snapshotMount struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadWrite   bool   `json:"read_write"`
}

type snapshotGUI struct {
	Requested config.GUIIntent `json:"requested"`
	Effective string           `json:"effective"`
	Deployed  string           `json:"deployed"`
}

type snapshotCompose struct {
	Command        map[string]any    `json:"command"`
	Files          []string          `json:"files"`
	Services       []string          `json:"services"`
	DesiredHashes  map[string]string `json:"desired_hashes"`
	DeployedHashes map[string]string `json:"deployed_hashes"`
	Drift          string            `json:"drift"`
	DriftDetail    string            `json:"drift_detail"`
	Error          string            `json:"error"`
}

type snapshotSession struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type snapshotDocker struct {
	Available bool   `json:"available"`
	Error     string `json:"error"`
}

func printSnapshot(ctx context.Context, streams Streams, cfg config.Config, workspace identity.Workspace, candidate *containerruntime.Container, containers []containerruntime.Container, legacy *legacyguard.Match, dockerErr, ownershipErr error, runner command.Runner, docker containerruntime.Docker) int {
	format := "human"
	if len(cfg.Command) > 1 {
		switch cfg.Command[1] {
		case "--format=json":
			format = "json"
		case "--format=human":
		default:
			return fail(streams.Err, 2, "info accepts only --format=human or --format=json")
		}
	}

	commandSummary := redactedComposeCommand(cfg.ComposeCommand)
	s := snapshot{
		Schema:    1,
		Launcher:  snapshotLauncher{Version: Version, Commit: Commit, Schema: identity.RuntimeSchemaVersion},
		Workspace: workspace,
		Project:   snapshotProject{Name: workspace.Project, Service: "hcorral", Container: workspace.Project, Others: []snapshotOtherProject{}},
		Configuration: snapshotConfig{
			Harness:          cfg.Harness,
			Image:            cfg.Image,
			UserConfigFile:   cfg.ConfigFile,
			Warnings:         nonNilStrings(cfg.Warnings),
			StateMode:        cfg.StateMode,
			StateVolume:      stateVolumeName(cfg, workspace),
			GUI:              cfg.GUI,
			ComposeCommand:   commandSummary,
			ComposeFiles:     nonNilStrings(cfg.ComposeFiles),
			ExtraVolumes:     nonNilStrings(cfg.ExtraVolumes),
			ContainerHome:    cfg.ContainerHome,
			Workdir:          cfg.Workdir,
			UpdateCheck:      cfg.UpdateCheck,
			WaitTimeout:      cfg.WaitTimeoutSeconds,
			ProgressInterval: cfg.ProgressIntervalSecond,
			Session:          cfg.Session,
			AutoAttach:       cfg.AutoAttach,
			Sources:          copyStringMap(cfg.Sources),
		},
		Ownership: snapshotOwnership{Status: "absent"},
		MyCodex:   snapshotMyCodex{Status: "clear", Conflict: legacy},
		Container: snapshotContainer{Status: "absent"},
		Image:     snapshotImage{SelectedReference: cfg.Image, SelectedDigests: []string{}, DeployedDigests: []string{}},
		State: snapshotState{
			Mode:    cfg.StateMode,
			Volume:  stateVolumeName(cfg, workspace),
			Managed: cfg.StateMode != config.StateCustom,
			Removal: snapshotRemoval{Action: "unknown", Target: stateVolumeName(cfg, workspace), ComposeProject: workspace.Project, ComposeVolumes: []string{}, ComposeNetworks: []string{}, PostComposeVolumes: []string{}, RetainedExternalVolumes: []string{}, RetainedExternalNetworks: []string{}, PreflightRefusalReasons: []string{}},
		},
		Mounts:  []snapshotMount{},
		GUI:     snapshotGUI{Requested: cfg.GUI, Effective: effectiveGUI(cfg, candidate), Deployed: deployedGUI(candidate)},
		Compose: snapshotCompose{Command: commandSummary, Files: nonNilStrings(cfg.ComposeFiles), Services: []string{}, DesiredHashes: map[string]string{}, DeployedHashes: map[string]string{}, Drift: "unknown"},
		Session: snapshotSession{Name: cfg.Session, Status: "absent"},
		Update:  update.Facts{Enabled: cfg.UpdateCheck, Pinned: !strings.HasSuffix(cfg.Image, ":latest"), LookupStatus: "unavailable", LookupErrorKind: "docker"},
		Docker:  snapshotDocker{Available: dockerErr == nil},
	}
	if legacy != nil {
		s.MyCodex.Status = "conflict"
	}
	if dockerErr != nil {
		s.Docker.Error = dockerErr.Error()
		s.State.Removal.Reason = "Docker unavailable"
		s.Compose.Error = "Docker unavailable"
	} else {
		populateDockerSnapshot(ctx, &s, cfg, workspace, candidate, containers, ownershipErr, runner, docker)
	}

	if format == "json" {
		encoder := json.NewEncoder(streams.Out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(s); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		return 0
	}
	printHumanSnapshot(streams.Out, s)
	return 0
}

func populateDockerSnapshot(ctx context.Context, s *snapshot, cfg config.Config, workspace identity.Workspace, candidate *containerruntime.Container, containers []containerruntime.Container, ownershipErr error, runner command.Runner, docker containerruntime.Docker) {
	for _, container := range containers {
		if container.Config.Labels[identity.LabelWorkspaceID] != workspace.FullID {
			continue
		}
		project := container.Config.Labels["com.docker.compose.project"]
		if project == "" || project == workspace.Project {
			continue
		}
		s.Project.Others = append(s.Project.Others, snapshotOtherProject{Name: project, Harness: container.Config.Labels[identity.LabelHarnessType], CorralID: container.Config.Labels[identity.LabelCorralID], Status: container.State.Status})
	}
	sort.Slice(s.Project.Others, func(i, j int) bool { return s.Project.Others[i].Name < s.Project.Others[j].Name })
	if image, err := docker.InspectImage(ctx, cfg.Image); err == nil && image != nil {
		s.Image.SelectedID = image.ID
		s.Image.SelectedDigests = nonNilStrings(image.RepoDigests)
	}
	managed := candidate
	if ownershipErr != nil {
		managed = nil
		s.Ownership.Status = "collision"
		s.Ownership.Error = ownershipErr.Error()
	}
	if candidate != nil {
		if ownershipErr == nil {
			s.Ownership = snapshotOwnership{
				Status:          "verified",
				WorkspaceID:     candidate.Config.Labels[identity.LabelWorkspaceID],
				WorkspaceScheme: candidate.Config.Labels[identity.LabelWorkspaceScheme],
				RuntimeSchema:   candidate.Config.Labels[identity.LabelRuntimeSchema],
			}
		}
		s.Container = snapshotContainer{Status: candidate.State.Status, ID: candidate.ID, StartedAt: candidate.State.Started, Image: candidate.Config.Image}
		s.Image.DeployedReference = candidate.Config.Image
		if image, err := docker.InspectImage(ctx, candidate.Config.Image); err == nil && image != nil {
			s.Image.DeployedID = image.ID
			s.Image.DeployedDigests = nonNilStrings(image.RepoDigests)
		}
		for _, mount := range candidate.Mounts {
			s.Mounts = append(s.Mounts, snapshotMount{Type: mount.Type, Name: mount.Name, Source: mount.Source, Destination: mount.Destination, ReadWrite: mount.RW})
		}
		if ownershipErr == nil && candidate.State.Running {
			if sessionReady(ctx, docker, cfg, workspace.Project) {
				s.Session.Status = "present"
			} else {
				s.Session.Status = "missing"
			}
		} else if ownershipErr == nil {
			s.Session.Status = "container-stopped"
		} else {
			s.Session.Status = "not-inspected-unowned"
		}
	}

	volume, volumeErr := docker.InspectVolume(ctx, s.State.Volume)
	if volumeErr == nil {
		exists := volume != nil
		s.State.Exists = &exists
		s.State.Removal = removalSnapshot(ctx, docker, cfg, workspace, containers, exists)
	} else {
		s.State.Removal.Reason = "volume inspection failed"
	}

	selection, assets, generated, project, err := prepareProject(ctx, cfg, workspace, managed, Streams{Out: ioDiscard{}, Err: ioDiscard{}}, runner)
	if generated.Path != "" {
		defer generated.Cleanup()
	}
	if err != nil {
		s.Compose.Error = "project preparation failed"
	} else {
		s.GUI.Effective = selection.Mode
		s.Compose.Files = append([]string{assets.Base}, cfg.ComposeFiles...)
		if selection.File != "" {
			s.Compose.Files = append([]string{assets.Base, selection.File}, cfg.ComposeFiles...)
		}
		if generated.Path != "" {
			s.Compose.Files = append(s.Compose.Files, generated.Path)
		}
		rendered, renderErr := project.Render(ctx)
		if renderErr != nil {
			s.Compose.Error = "rendered project unavailable or invalid"
		} else {
			if service, ok := rendered.Services["hcorral"]; ok {
				s.Image.RenderedReference = service.Image
			}
			for service := range rendered.Services {
				s.Compose.Services = append(s.Compose.Services, service)
			}
			sort.Strings(s.Compose.Services)
			s.Compose.DesiredHashes = copyStringMap(rendered.Hashes)
			for _, container := range containers {
				if container.Config.Labels["com.docker.compose.project"] == workspace.Project {
					s.Compose.DeployedHashes[container.Config.Labels["com.docker.compose.service"]] = container.Config.Labels["com.docker.compose.config-hash"]
				}
			}
			s.Compose.Drift, s.Compose.DriftDetail = compareDrift(rendered, s.Compose.DeployedHashes)
			populateRemovalSet(ctx, &s.State.Removal, docker, rendered, cfg, workspace)
			if networkErr := validateComposeNetworkOwnership(ctx, docker, rendered, workspace); networkErr != nil {
				s.Compose.Error = "Compose network ownership is ambiguous"
			} else if volumeErr := validateComposeVolumeOwnership(ctx, docker, rendered, workspace); volumeErr != nil {
				s.Compose.Error = "Compose volume ownership is ambiguous"
			}
		}
	}

	s.Update = update.Checker{Docker: docker, LauncherVersion: Version}.Inspect(ctx, cfg, managed)
}

func removalSnapshot(ctx context.Context, docker containerruntime.Docker, cfg config.Config, workspace identity.Workspace, containers []containerruntime.Container, exists bool) snapshotRemoval {
	result := snapshotRemoval{Action: "retain", Target: stateVolumeName(cfg, workspace), ComposeProject: workspace.Project, ComposeVolumes: []string{}, ComposeNetworks: []string{}, PostComposeVolumes: []string{}, RetainedExternalVolumes: []string{}, RetainedExternalNetworks: []string{}, PreflightRefusalReasons: []string{}}
	if !exists {
		result.Action, result.Reason = "nothing", "volume absent"
		return result
	}
	if cfg.StateMode == config.StateCustom {
		result.Reason = "user-managed custom volume"
		return result
	}
	if cfg.StateMode == config.StateShared {
		result.Reason = "global shared state is retained by down -v"
		return result
	}
	remove, _, err := planManagedStateRemoval(ctx, docker, cfg, workspace, containers)
	if err != nil {
		result.Action, result.Reason = "refuse", "ownership or reference check failed"
		return result
	}
	if remove {
		result.Action, result.Reason = "remove", "launcher-managed volume is safe to remove"
	} else {
		result.Reason = "workspace-private volume remains referenced"
	}
	return result
}

func populateRemovalSet(ctx context.Context, result *snapshotRemoval, docker containerruntime.Docker, rendered compose.Rendered, cfg config.Config, workspace identity.Workspace) {
	stateName := stateVolumeName(cfg, workspace)
	for logicalName, definition := range rendered.Volumes {
		if definition.External {
			if definition.Name != stateName {
				result.RetainedExternalVolumes = append(result.RetainedExternalVolumes, definition.Name)
			}
			continue
		}
		result.ComposeVolumes = append(result.ComposeVolumes, definition.Name)
		volume, err := docker.InspectVolume(ctx, definition.Name)
		if err != nil {
			result.PreflightRefusalReasons = append(result.PreflightRefusalReasons, "inspection failed for "+definition.Name)
		} else if volume != nil && (volume.Labels["com.docker.compose.project"] != workspace.Project || volume.Labels["com.docker.compose.volume"] != logicalName) {
			result.PreflightRefusalReasons = append(result.PreflightRefusalReasons, "ownership is ambiguous for "+definition.Name)
		}
	}
	for logicalName, definition := range rendered.Networks {
		if definition.External {
			result.RetainedExternalNetworks = append(result.RetainedExternalNetworks, definition.Name)
		} else {
			result.ComposeNetworks = append(result.ComposeNetworks, definition.Name)
			network, err := docker.InspectNetwork(ctx, definition.Name)
			if err != nil {
				result.PreflightRefusalReasons = append(result.PreflightRefusalReasons, "inspection failed for "+definition.Name)
			} else if network != nil && (network.Labels["com.docker.compose.project"] != workspace.Project || network.Labels["com.docker.compose.network"] != logicalName) {
				result.PreflightRefusalReasons = append(result.PreflightRefusalReasons, "ownership is ambiguous for "+definition.Name)
			}
		}
	}
	if result.Action == "remove" {
		result.PostComposeVolumes = append(result.PostComposeVolumes, stateName)
	} else if result.Action == "retain" {
		result.RetainedExternalVolumes = append(result.RetainedExternalVolumes, stateName)
	} else if result.Action == "refuse" {
		result.PreflightRefusalReasons = append(result.PreflightRefusalReasons, "state volume ownership or references are ambiguous")
	}
	sort.Strings(result.ComposeVolumes)
	sort.Strings(result.ComposeNetworks)
	sort.Strings(result.PostComposeVolumes)
	sort.Strings(result.RetainedExternalVolumes)
	sort.Strings(result.RetainedExternalNetworks)
	sort.Strings(result.PreflightRefusalReasons)
}

func compareDrift(rendered compose.Rendered, deployed map[string]string) (string, string) {
	if len(rendered.Hashes) == 0 {
		return "unknown", "Compose config hash unavailable"
	}
	if len(deployed) == 0 {
		return "not-deployed", "no deployed services"
	}
	if len(deployed) != len(rendered.Services) {
		return "present", "service set differs"
	}
	for service := range rendered.Services {
		if rendered.Hashes[service] == "" || deployed[service] != rendered.Hashes[service] {
			return "present", "service " + service + " hash differs"
		}
	}
	return "none", ""
}

func effectiveGUI(cfg config.Config, candidate *containerruntime.Container) string {
	if cfg.GUI.Specified {
		return cfg.GUI.Mode
	}
	return deployedGUI(candidate)
}

func redactedComposeCommand(argv []string) map[string]any {
	if len(argv) == 2 && argv[0] == "docker" && argv[1] == "compose" {
		return map[string]any{"display": "docker compose"}
	}
	executable := ""
	if len(argv) > 0 {
		executable = filepath.Base(argv[0])
	}
	return map[string]any{"executable": executable, "argument_count": max(0, len(argv)-1)}
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return append([]string(nil), values...)
}

func copyStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func printHumanSnapshot(out interface{ Write([]byte) (int, error) }, s snapshot) {
	fmt.Fprintf(out, "Harness Corral %s\n", s.Launcher.Version)
	fmt.Fprintf(out, "Workspace: %s\nWorkspace ID: %s\n", s.Workspace.Path, s.Workspace.FullID)
	fmt.Fprintf(out, "Harness: %s\nCorral ID: %s\nProject: %s\nContainer: %s (%s)\n", s.Configuration.Harness, s.Workspace.CorralID, s.Project.Name, s.Project.Container, s.Container.Status)
	if len(s.Project.Others) > 0 {
		fmt.Fprintf(out, "Other workspace corrals: %v\n", s.Project.Others)
	}
	fmt.Fprintf(out, "Ownership: %s\nImage: selected=%s rendered=%s deployed=%s\n", s.Ownership.Status, s.Image.SelectedReference, valueOrEmpty(s.Image.RenderedReference, "unavailable"), valueOrEmpty(s.Image.DeployedReference, "absent"))
	fmt.Fprintf(out, "State: %s (%s; %s)\n", s.State.Mode, s.State.Volume, s.State.Removal.Action)
	fmt.Fprintf(out, "Removal: project=%s compose-networks=%v compose-volumes=%v post-compose-volumes=%v retained-volumes=%v retained-networks=%v refusals=%v\n", s.State.Removal.ComposeProject, s.State.Removal.ComposeNetworks, s.State.Removal.ComposeVolumes, s.State.Removal.PostComposeVolumes, s.State.Removal.RetainedExternalVolumes, s.State.Removal.RetainedExternalNetworks, s.State.Removal.PreflightRefusalReasons)
	fmt.Fprintf(out, "GUI: requested=%s effective=%s deployed=%s\n", valueOrEmpty(s.GUI.Requested.Mode, "unspecified"), s.GUI.Effective, s.GUI.Deployed)
	fmt.Fprintf(out, "Compose: services=%v drift=%s\nSession: %s\n", s.Compose.Services, s.Compose.Drift, s.Session.Status)
	fmt.Fprintf(out, "Harness version: current=%s selected=%s upstream=%s\n", valueOrEmpty(s.Update.Current, "unknown"), valueOrEmpty(s.Update.Selected, "unknown"), valueOrEmpty(s.Update.Latest, "unknown"))
	if s.MyCodex.Conflict != nil {
		fmt.Fprintf(out, "myCodex conflict: %s (%s; %s)\n", s.MyCodex.Conflict.Container, s.MyCodex.Conflict.State, s.MyCodex.Conflict.Reason)
	}
	if !s.Docker.Available {
		fmt.Fprintf(out, "Docker: unavailable (%s)\n", s.Docker.Error)
	}
}
