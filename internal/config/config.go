package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/infrasecture/hcorral/internal/harness"
	"github.com/pelletier/go-toml/v2"
)

var (
	sessionPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	harnessPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

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
	Harness                string
	Image                  string
	ConfigFile             string
	Warnings               []string
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
	CallerDir, HomeDir, Platform string
	Getenv                       func(string) string
	Environ                      func() []string
}
type fileConfig struct {
	DefaultHarness string                       `toml:"default_harness"`
	Harness        map[string]fileHarnessConfig `toml:"harness"`
}
type fileHarnessConfig struct {
	Image string `toml:"image"`
}

var knownEnvironment = map[string]bool{
	"HCORRAL_HARNESS": true, "HCORRAL_IMAGE": true, "HCORRAL_WORKSPACE": true,
	"HCORRAL_PROJECT_NAME": true, "HCORRAL_STATE_VOLUME_NAME": true, "HCORRAL_PRIVATE_ENV": true,
	"HCORRAL_GUI": true, "HCORRAL_COMPOSE_COMMAND": true, "HCORRAL_COMPOSE_FILES": true,
	"HCORRAL_CONTAINER_HOME": true, "HCORRAL_WORKDIR": true, "HCORRAL_UPDATE_CHECK": true,
	"HCORRAL_WAIT_TIMEOUT_SECONDS": true, "HCORRAL_STARTUP_PROGRESS_INTERVAL_SECONDS": true,
	"HCORRAL_BYOBU_SESSION": true, "HCORRAL_AUTO_ATTACH": true,
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
	path, err := userConfigPath(options)
	if err != nil {
		return Config{}, err
	}
	file, err := readUserConfig(path)
	if err != nil {
		return Config{}, err
	}

	selectedHarness, harnessSource := "codex", "default"
	if file.DefaultHarness != "" {
		selectedHarness, harnessSource = file.DefaultHarness, "user-config"
	}
	if value := options.Getenv("HCORRAL_HARNESS"); value != "" {
		selectedHarness, harnessSource = value, "environment"
	}
	cfg := Config{
		CallerDir: options.CallerDir, Workspace: valueOr(options.Getenv("HCORRAL_WORKSPACE"), options.CallerDir),
		ProjectName: options.Getenv("HCORRAL_PROJECT_NAME"), Harness: selectedHarness, ConfigFile: path,
		StateMode: StateShared, StateVolumeName: options.Getenv("HCORRAL_STATE_VOLUME_NAME"),
		ComposeCommand: []string{"docker", "compose"}, ContainerHome: valueOr(options.Getenv("HCORRAL_CONTAINER_HOME"), options.HomeDir),
		Workdir: options.Getenv("HCORRAL_WORKDIR"), UpdateCheck: true, WaitTimeoutSeconds: 30,
		ProgressIntervalSecond: 2, Session: valueOr(options.Getenv("HCORRAL_BYOBU_SESSION"), "hcorral"), Platform: options.Platform,
		Sources: map[string]string{
			"workspace": source(options.Getenv("HCORRAL_WORKSPACE") != ""), "project_name": source(options.Getenv("HCORRAL_PROJECT_NAME") != ""),
			"harness": harnessSource, "image": "default", "state": "default", "gui": "default",
			"compose_command": source(options.Getenv("HCORRAL_COMPOSE_COMMAND") != ""), "compose_files": source(options.Getenv("HCORRAL_COMPOSE_FILES") != ""),
			"extra_volumes": "default", "container_home": source(options.Getenv("HCORRAL_CONTAINER_HOME") != ""), "workdir": source(options.Getenv("HCORRAL_WORKDIR") != ""),
			"update_check": source(options.Getenv("HCORRAL_UPDATE_CHECK") != ""), "wait_timeout": source(options.Getenv("HCORRAL_WAIT_TIMEOUT_SECONDS") != ""),
			"progress_interval": source(options.Getenv("HCORRAL_STARTUP_PROGRESS_INTERVAL_SECONDS") != ""),
			"session":           source(options.Getenv("HCORRAL_BYOBU_SESSION") != ""), "auto_attach": source(options.Getenv("HCORRAL_AUTO_ATTACH") != ""),
		},
	}
	if options.Environ != nil {
		cfg.Warnings = unknownEnvironmentWarnings(options.Environ())
	}

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
		cfg.StateSpecified, cfg.Sources["state"] = true, "environment"
		private, parseErr := parseBool("HCORRAL_PRIVATE_ENV", raw)
		if parseErr != nil {
			return Config{}, parseErr
		}
		if private {
			cfg.StateMode = StatePrivate
		}
	}
	if cfg.StateVolumeName != "" {
		cfg.StateSpecified, cfg.Sources["state"] = true, "environment"
		if cfg.StateMode == StatePrivate {
			return Config{}, errors.New("HCORRAL_PRIVATE_ENV conflicts with HCORRAL_STATE_VOLUME_NAME")
		}
		cfg.StateMode = StateCustom
	}
	if raw := options.Getenv("HCORRAL_GUI"); raw != "" {
		cfg.GUI, cfg.Sources["gui"] = GUIIntent{Specified: true, Mode: raw}, "environment"
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

	privateFlag, stateFlag, cliGUIFlag := false, false, false
	for index := 0; index < len(args); {
		arg := args[index]
		switch {
		case arg == "--":
			cfg.Command, index = append([]string(nil), args[index+1:]...), len(args)
		case arg == "-h" || arg == "--help":
			cfg.Command, index = []string{"help"}, len(args)
		case arg == "--harness":
			value, next, e := optionValue(args, index, arg)
			if e != nil {
				return Config{}, e
			}
			cfg.Harness, cfg.Sources["harness"], index = value, "cli", next
		case strings.HasPrefix(arg, "--harness="):
			cfg.Harness, cfg.Sources["harness"], index = strings.TrimPrefix(arg, "--harness="), "cli", index+1
		case arg == "--image":
			value, next, e := optionValue(args, index, arg)
			if e != nil {
				return Config{}, e
			}
			cfg.Image, cfg.Sources["image"], index = value, "cli", next
		case strings.HasPrefix(arg, "--image="):
			cfg.Image, cfg.Sources["image"], index = strings.TrimPrefix(arg, "--image="), "cli", index+1
		case arg == "--workspace":
			value, next, e := optionValue(args, index, arg)
			if e != nil {
				return Config{}, e
			}
			cfg.Workspace, cfg.Sources["workspace"], index = value, "cli", next
		case strings.HasPrefix(arg, "--workspace="):
			cfg.Workspace, cfg.Sources["workspace"], index = strings.TrimPrefix(arg, "--workspace="), "cli", index+1
		case arg == "--project-name":
			value, next, e := optionValue(args, index, arg)
			if e != nil {
				return Config{}, e
			}
			cfg.ProjectName, cfg.Sources["project_name"], index = value, "cli", next
		case strings.HasPrefix(arg, "--project-name="):
			cfg.ProjectName, cfg.Sources["project_name"], index = strings.TrimPrefix(arg, "--project-name="), "cli", index+1
		case arg == "--state-volume":
			value, next, e := optionValue(args, index, arg)
			if e != nil {
				return Config{}, e
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
			cfg.GUI, cfg.Sources["gui"], index = GUIIntent{Specified: true, Mode: "auto"}, "cli", index+1
		case strings.HasPrefix(arg, "--gui="):
			if cliGUIFlag {
				return Config{}, errors.New("GUI mode was specified more than once")
			}
			cliGUIFlag = true
			cfg.GUI, cfg.Sources["gui"], index = GUIIntent{Specified: true, Mode: strings.TrimPrefix(arg, "--gui=")}, "cli", index+1
		case arg == "--no-gui":
			if cliGUIFlag {
				return Config{}, errors.New("GUI mode was specified more than once")
			}
			cliGUIFlag = true
			cfg.GUI, cfg.Sources["gui"], index = GUIIntent{Specified: true, Mode: "none"}, "cli", index+1
		case arg == "-v" || arg == "--volume":
			value, next, e := optionValue(args, index, arg)
			if e != nil {
				return Config{}, e
			}
			cfg.ExtraVolumes, cfg.Sources["extra_volumes"], index = append(cfg.ExtraVolumes, value), "cli", next
		case strings.HasPrefix(arg, "--volume="):
			cfg.ExtraVolumes, cfg.Sources["extra_volumes"], index = append(cfg.ExtraVolumes, strings.TrimPrefix(arg, "--volume=")), "cli", index+1
		case arg == "-f" || arg == "--compose-file":
			value, next, e := optionValue(args, index, arg)
			if e != nil {
				return Config{}, e
			}
			cfg.ComposeFiles, cfg.Sources["compose_files"], index = append(cfg.ComposeFiles, value), appendedSource(cfg.Sources["compose_files"]), next
		case strings.HasPrefix(arg, "--compose-file="):
			cfg.ComposeFiles, cfg.Sources["compose_files"], index = append(cfg.ComposeFiles, strings.TrimPrefix(arg, "--compose-file=")), appendedSource(cfg.Sources["compose_files"]), index+1
		default:
			cfg.Command, index = append([]string(nil), args[index:]...), len(args)
		}
	}

	cfg.Harness = harness.Normalize(cfg.Harness)
	if privateFlag {
		if stateFlag {
			return Config{}, errors.New("--private-env conflicts with explicit state volume")
		}
		cfg.StateVolumeName, cfg.StateMode, cfg.StateSpecified = "", StatePrivate, true
	}
	if stateFlag {
		if cfg.StateVolumeName == "" {
			return Config{}, errors.New("--state-volume must not be empty")
		}
		cfg.StateMode, cfg.StateSpecified = StateCustom, true
	}
	if cfg.Image == "" {
		if value := options.Getenv("HCORRAL_IMAGE"); value != "" {
			cfg.Image, cfg.Sources["image"] = value, "environment"
		}
	}
	if cfg.Image == "" {
		if selected, ok := file.Harness[cfg.Harness]; ok && selected.Image != "" {
			cfg.Image, cfg.Sources["image"] = selected.Image, "user-config"
		}
	}
	if cfg.Image == "" {
		if value, ok := harness.DefaultImage(cfg.Harness); ok {
			cfg.Image = value
		} else {
			return Config{}, fmt.Errorf("unknown harness %q requires --image, HCORRAL_IMAGE, or a user-config image", cfg.Harness)
		}
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

func userConfigPath(options ParseOptions) (string, error) {
	if options.HomeDir == "" {
		return "", errors.New("home directory is empty")
	}
	if options.Platform == "darwin" {
		return filepath.Join(options.HomeDir, "Library", "Application Support", "hcorral", "config.toml"), nil
	}
	root := options.Getenv("XDG_CONFIG_HOME")
	if root == "" {
		root = filepath.Join(options.HomeDir, ".config")
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("XDG_CONFIG_HOME must be absolute")
	}
	return filepath.Join(root, "hcorral", "config.toml"), nil
}
func readUserConfig(path string) (fileConfig, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fileConfig{}, nil
	}
	if err != nil {
		return fileConfig{}, fmt.Errorf("read user config %s: %w", path, err)
	}
	var result fileConfig
	decoder := toml.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return fileConfig{}, fmt.Errorf("parse user config %s: %w", path, err)
	}
	return result, nil
}
func unknownEnvironmentWarnings(environ []string) []string {
	seen := map[string]bool{}
	for _, entry := range environ {
		key := strings.SplitN(entry, "=", 2)[0]
		if strings.HasPrefix(key, "HCORRAL_") && !knownEnvironment[key] {
			seen[key] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, "unknown environment variable "+key+" was ignored")
	}
	return result
}
func validate(cfg *Config) error {
	if cfg.Workspace == "" {
		return errors.New("workspace must not be empty")
	}
	if !harnessPattern.MatchString(cfg.Harness) {
		return fmt.Errorf("harness type must match [a-z][a-z0-9_]{0,31}: %q", cfg.Harness)
	}
	if cfg.Image == "" || strings.ContainsRune(cfg.Image, 0) || strings.ContainsAny(cfg.Image, " \t\r\n") {
		return errors.New("image must be a non-empty OCI reference without whitespace")
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
