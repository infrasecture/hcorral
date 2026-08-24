package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

var sessionPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

type StateMode string

const (
	StateShared  StateMode = "shared"
	StatePrivate StateMode = "private"
	StateCustom  StateMode = "custom"
)

type GUIIntent struct {
	Specified bool   `json:"specified"`
	Mode      string `json:"mode"`
}

type Config struct {
	CallerDir              string
	Workspace              string
	ProjectName            string
	ImageName              string
	ImageTag               string
	StateMode              StateMode
	StateVolumeName        string
	StateSpecified         bool
	GUI                    GUIIntent
	ComposeCommand         []string
	ComposeFiles           []string
	ExtraVolumes           []string
	ContainerHome          string
	Workdir                string
	UpdateCheck            bool
	WaitTimeoutSeconds     int
	ProgressIntervalSecond int
	Session                string
	AutoAttach             bool
	Command                []string
	Platform               string
	Sources                map[string]string
}

type ParseOptions struct {
	CallerDir string
	HomeDir   string
	Platform  string
	Getenv    func(string) string
}

func Parse(args []string, options ParseOptions) (Config, error) {
	if options.Getenv == nil {
		return Config{}, errors.New("configuration environment reader is nil")
	}
	if options.CallerDir == "" {
		return Config{}, errors.New("caller directory is empty")
	}
	if options.Platform == "" {
		options.Platform = runtime.GOOS
	}

	cfg := Config{
		CallerDir:              options.CallerDir,
		Workspace:              valueOr(options.Getenv("HCORRAL_WORKSPACE"), options.CallerDir),
		ProjectName:            options.Getenv("HCORRAL_PROJECT_NAME"),
		ImageName:              valueOr(options.Getenv("HCORRAL_IMAGE_NAME"), "ghcr.io/infrasecture/hcorral"),
		ImageTag:               valueOr(options.Getenv("HCORRAL_IMAGE_TAG"), "latest"),
		StateMode:              StateShared,
		StateVolumeName:        options.Getenv("HCORRAL_STATE_VOLUME_NAME"),
		ComposeCommand:         []string{"docker", "compose"},
		ContainerHome:          valueOr(options.Getenv("HCORRAL_CONTAINER_HOME"), options.HomeDir),
		Workdir:                options.Getenv("HCORRAL_WORKDIR"),
		UpdateCheck:            true,
		WaitTimeoutSeconds:     30,
		ProgressIntervalSecond: 2,
		Session:                valueOr(options.Getenv("HCORRAL_BYOBU_SESSION"), "hcorral"),
		Platform:               options.Platform,
		Sources: map[string]string{
			"workspace":         source(options.Getenv("HCORRAL_WORKSPACE") != ""),
			"project_name":      source(options.Getenv("HCORRAL_PROJECT_NAME") != ""),
			"image_name":        source(options.Getenv("HCORRAL_IMAGE_NAME") != ""),
			"image_tag":         source(options.Getenv("HCORRAL_IMAGE_TAG") != ""),
			"state":             "default",
			"gui":               "default",
			"compose_command":   source(options.Getenv("HCORRAL_COMPOSE_COMMAND") != ""),
			"compose_files":     source(options.Getenv("HCORRAL_COMPOSE_FILES") != ""),
			"extra_volumes":     "default",
			"container_home":    source(options.Getenv("HCORRAL_CONTAINER_HOME") != ""),
			"workdir":           source(options.Getenv("HCORRAL_WORKDIR") != ""),
			"update_check":      source(options.Getenv("HCORRAL_UPDATE_CHECK") != ""),
			"wait_timeout":      source(options.Getenv("HCORRAL_WAIT_TIMEOUT_SECONDS") != ""),
			"progress_interval": source(options.Getenv("HCORRAL_STARTUP_PROGRESS_INTERVAL_SECONDS") != ""),
			"session":           source(options.Getenv("HCORRAL_BYOBU_SESSION") != ""),
			"auto_attach":       source(options.Getenv("HCORRAL_AUTO_ATTACH") != ""),
		},
	}

	var err error
	if raw := options.Getenv("HCORRAL_COMPOSE_COMMAND"); raw != "" {
		cfg.ComposeCommand, err = parseStringArray("HCORRAL_COMPOSE_COMMAND", raw, false)
		if err != nil {
			return Config{}, err
		}
	}
	if raw := options.Getenv("HCORRAL_COMPOSE_FILES"); raw != "" {
		cfg.ComposeFiles, err = parseStringArray("HCORRAL_COMPOSE_FILES", raw, true)
		if err != nil {
			return Config{}, err
		}
	}
	if raw := options.Getenv("HCORRAL_PRIVATE_ENV"); raw != "" {
		cfg.StateSpecified = true
		cfg.Sources["state"] = "environment"
		private, parseErr := parseBool("HCORRAL_PRIVATE_ENV", raw)
		if parseErr != nil {
			return Config{}, parseErr
		}
		if private {
			cfg.StateMode = StatePrivate
		}
	}
	if cfg.StateVolumeName != "" {
		cfg.StateSpecified = true
		cfg.Sources["state"] = "environment"
		if cfg.StateMode == StatePrivate {
			return Config{}, errors.New("HCORRAL_PRIVATE_ENV conflicts with HCORRAL_STATE_VOLUME_NAME")
		}
		cfg.StateMode = StateCustom
	}
	if raw := options.Getenv("HCORRAL_GUI"); raw != "" {
		cfg.GUI = GUIIntent{Specified: true, Mode: raw}
		cfg.Sources["gui"] = "environment"
	}
	if raw := options.Getenv("HCORRAL_UPDATE_CHECK"); raw != "" {
		cfg.UpdateCheck, err = parseBool("HCORRAL_UPDATE_CHECK", raw)
		if err != nil {
			return Config{}, err
		}
	}
	if raw := options.Getenv("HCORRAL_AUTO_ATTACH"); raw != "" {
		cfg.AutoAttach, err = parseBool("HCORRAL_AUTO_ATTACH", raw)
		if err != nil {
			return Config{}, err
		}
	}
	if raw := options.Getenv("HCORRAL_WAIT_TIMEOUT_SECONDS"); raw != "" {
		cfg.WaitTimeoutSeconds, err = parsePositiveInt("HCORRAL_WAIT_TIMEOUT_SECONDS", raw)
		if err != nil {
			return Config{}, err
		}
	}
	if raw := options.Getenv("HCORRAL_STARTUP_PROGRESS_INTERVAL_SECONDS"); raw != "" {
		cfg.ProgressIntervalSecond, err = parsePositiveInt("HCORRAL_STARTUP_PROGRESS_INTERVAL_SECONDS", raw)
		if err != nil {
			return Config{}, err
		}
	}

	privateFlag := false
	stateFlag := false
	cliGUIFlag := false
	for index := 0; index < len(args); {
		arg := args[index]
		switch {
		case arg == "--":
			cfg.Command = append([]string(nil), args[index+1:]...)
			index = len(args)
		case arg == "-h" || arg == "--help":
			cfg.Command = []string{"help"}
			index = len(args)
		case arg == "--workspace":
			value, next, valueErr := optionValue(args, index, arg)
			if valueErr != nil {
				return Config{}, valueErr
			}
			cfg.Workspace, cfg.Sources["workspace"], index = value, "cli", next
		case strings.HasPrefix(arg, "--workspace="):
			cfg.Workspace, cfg.Sources["workspace"], index = strings.TrimPrefix(arg, "--workspace="), "cli", index+1
		case arg == "--project-name":
			value, next, valueErr := optionValue(args, index, arg)
			if valueErr != nil {
				return Config{}, valueErr
			}
			cfg.ProjectName, cfg.Sources["project_name"], index = value, "cli", next
		case strings.HasPrefix(arg, "--project-name="):
			cfg.ProjectName, cfg.Sources["project_name"], index = strings.TrimPrefix(arg, "--project-name="), "cli", index+1
		case arg == "--state-volume":
			value, next, valueErr := optionValue(args, index, arg)
			if valueErr != nil {
				return Config{}, valueErr
			}
			cfg.StateVolumeName, cfg.Sources["state"], stateFlag, index = value, "cli", true, next
		case strings.HasPrefix(arg, "--state-volume="):
			cfg.StateVolumeName, cfg.Sources["state"], stateFlag, index = strings.TrimPrefix(arg, "--state-volume="), "cli", true, index+1
		case arg == "--private-env":
			privateFlag, cfg.Sources["state"], index = true, "cli", index+1
		case arg == "--gui":
			if cliGUIFlag {
				return Config{}, errors.New("GUI mode was specified more than once")
			}
			cliGUIFlag = true
			cfg.GUI, index = GUIIntent{Specified: true, Mode: "auto"}, index+1
			cfg.Sources["gui"] = "cli"
		case strings.HasPrefix(arg, "--gui="):
			if cliGUIFlag {
				return Config{}, errors.New("GUI mode was specified more than once")
			}
			cliGUIFlag = true
			cfg.GUI, index = GUIIntent{Specified: true, Mode: strings.TrimPrefix(arg, "--gui=")}, index+1
			cfg.Sources["gui"] = "cli"
		case arg == "--no-gui":
			if cliGUIFlag {
				return Config{}, errors.New("GUI mode was specified more than once")
			}
			cliGUIFlag = true
			cfg.GUI, index = GUIIntent{Specified: true, Mode: "none"}, index+1
			cfg.Sources["gui"] = "cli"
		case arg == "-v" || arg == "--volume":
			value, next, valueErr := optionValue(args, index, arg)
			if valueErr != nil {
				return Config{}, valueErr
			}
			cfg.ExtraVolumes, cfg.Sources["extra_volumes"], index = append(cfg.ExtraVolumes, value), "cli", next
		case strings.HasPrefix(arg, "--volume="):
			cfg.ExtraVolumes, cfg.Sources["extra_volumes"], index = append(cfg.ExtraVolumes, strings.TrimPrefix(arg, "--volume=")), "cli", index+1
		case arg == "-f" || arg == "--compose-file":
			value, next, valueErr := optionValue(args, index, arg)
			if valueErr != nil {
				return Config{}, valueErr
			}
			cfg.ComposeFiles, cfg.Sources["compose_files"], index = append(cfg.ComposeFiles, value), appendedSource(cfg.Sources["compose_files"]), next
		case strings.HasPrefix(arg, "--compose-file="):
			cfg.ComposeFiles, cfg.Sources["compose_files"], index = append(cfg.ComposeFiles, strings.TrimPrefix(arg, "--compose-file=")), appendedSource(cfg.Sources["compose_files"]), index+1
		default:
			cfg.Command = append([]string(nil), args[index:]...)
			index = len(args)
		}
	}

	if privateFlag {
		if stateFlag {
			return Config{}, errors.New("--private-env conflicts with explicit state volume")
		}
		cfg.StateVolumeName = ""
		cfg.StateMode = StatePrivate
		cfg.StateSpecified = true
	}
	if stateFlag {
		if cfg.StateVolumeName == "" {
			return Config{}, errors.New("--state-volume must not be empty")
		}
		cfg.StateMode = StateCustom
		cfg.StateSpecified = true
	}
	if err := validate(&cfg); err != nil {
		return Config{}, err
	}
	cfg.Workspace = resolveFromCaller(cfg.CallerDir, cfg.Workspace)
	if cfg.Workdir == "" {
		cfg.Workdir = cfg.Workspace
	} else {
		cfg.Workdir = resolveFromCaller(cfg.CallerDir, cfg.Workdir)
	}
	for index, file := range cfg.ComposeFiles {
		cfg.ComposeFiles[index] = resolveFromCaller(cfg.CallerDir, file)
	}
	return cfg, nil
}

func validate(cfg *Config) error {
	if cfg.Workspace == "" {
		return errors.New("workspace must not be empty")
	}
	if cfg.ImageName == "" || cfg.ImageTag == "" {
		return errors.New("image name and tag must not be empty")
	}
	if cfg.ContainerHome == "" || !filepath.IsAbs(cfg.ContainerHome) {
		return errors.New("container home must be an absolute path")
	}
	if cfg.ProgressIntervalSecond > cfg.WaitTimeoutSeconds {
		return errors.New("startup progress interval must not exceed wait timeout")
	}
	if !sessionPattern.MatchString(cfg.Session) {
		return errors.New("tmux session name must match [A-Za-z0-9_.-]{1,64}")
	}
	if cfg.GUI.Specified {
		switch cfg.GUI.Mode {
		case "none", "auto", "x11", "wayland":
		default:
			return fmt.Errorf("GUI mode must be none, auto, x11, or wayland: %q", cfg.GUI.Mode)
		}
		if cfg.Platform == "darwin" && cfg.GUI.Mode != "none" {
			return fmt.Errorf("GUI mode %q is unsupported on macOS; use --no-gui or headless mode", cfg.GUI.Mode)
		}
	}
	return nil
}

func optionValue(args []string, index int, name string) (string, int, error) {
	if index+1 >= len(args) {
		return "", index, fmt.Errorf("%s requires a value", name)
	}
	if args[index+1] == "" {
		return "", index, fmt.Errorf("%s must not be empty", name)
	}
	return args[index+1], index + 2, nil
}

func parseStringArray(name, value string, allowEmpty bool) ([]string, error) {
	var result []string
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		return nil, fmt.Errorf("%s must be a JSON array of strings: %w", name, err)
	}
	if !allowEmpty && len(result) == 0 {
		return nil, fmt.Errorf("%s must not be empty", name)
	}
	for _, item := range result {
		if item == "" || strings.ContainsRune(item, 0) {
			return nil, fmt.Errorf("%s elements must be non-empty and contain no NUL bytes", name)
		}
	}
	return result, nil
}

func parseBool(name, value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false: %q", name, value)
	}
}

func parsePositiveInt(name, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer: %q", name, value)
	}
	return parsed, nil
}

func resolveFromCaller(caller, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(caller, value))
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func source(fromEnvironment bool) string {
	if fromEnvironment {
		return "environment"
	}
	return "default"
}

func appendedSource(previous string) string {
	if previous == "environment" || previous == "environment+cli" {
		return "environment+cli"
	}
	return "cli"
}
