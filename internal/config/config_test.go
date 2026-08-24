package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := Parse(nil, options(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workspace != "/work/current" || cfg.Harness != "codex" || cfg.Image != "ghcr.io/infrasecture/hcorral-codex:latest" {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.StateMode != StateShared || cfg.GUI.Specified || !reflect.DeepEqual(cfg.ComposeCommand, []string{"docker", "compose"}) {
		t.Fatalf("unexpected typed defaults: %#v", cfg)
	}
	for _, key := range []string{"workspace", "project_name", "harness", "image", "state", "gui", "compose_command", "compose_files", "extra_volumes", "container_home", "workdir", "update_check", "wait_timeout", "progress_interval", "session", "auto_attach"} {
		if cfg.Sources[key] != "default" {
			t.Errorf("source %s = %q, want default", key, cfg.Sources[key])
		}
	}
}

func TestHarnessAndImagePrecedenceWithUserConfig(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".config", "hcorral", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("default_harness = \"claude\"\n[harness.claude]\nimage = \"example/claude:approved\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HCORRAL_UNUSED_FUTURE": "1"}
	options := ParseOptions{CallerDir: "/work", HomeDir: home, Platform: "linux", Getenv: func(key string) string { return env[key] }, Environ: func() []string { return []string{"HCORRAL_UNUSED_FUTURE=1"} }}
	cfg, err := Parse(nil, options)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Harness != "claude" || cfg.Image != "example/claude:approved" || cfg.Sources["harness"] != "user-config" || cfg.Sources["image"] != "user-config" {
		t.Fatalf("user config not selected: %#v", cfg)
	}
	if len(cfg.Warnings) != 1 || !strings.Contains(cfg.Warnings[0], "HCORRAL_UNUSED_FUTURE") {
		t.Fatalf("unknown warning: %#v", cfg.Warnings)
	}
	cfg, err = Parse([]string{"--harness", "pi", "--image", "example/pi@sha256:abc"}, options)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Harness != "pi" || cfg.Image != "example/pi@sha256:abc" || cfg.Sources["harness"] != "cli" || cfg.Sources["image"] != "cli" {
		t.Fatalf("CLI precedence failed: %#v", cfg)
	}
}

func TestUnknownHarnessRequiresExplicitImage(t *testing.T) {
	_, err := Parse([]string{"--harness", "company_agent"}, options(nil))
	if err == nil || !strings.Contains(err.Error(), "requires --image") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParsePrecedenceAndOrderedArrays(t *testing.T) {
	t.Parallel()
	env := map[string]string{
		"HCORRAL_WORKSPACE":       "/env/workspace",
		"HCORRAL_COMPOSE_COMMAND": `["/opt/policy compose","compose"]`,
		"HCORRAL_COMPOSE_FILES":   `["env-a.yaml","env-b.yaml"]`,
		"HCORRAL_PRIVATE_ENV":     "false",
	}
	cfg, err := Parse([]string{"--workspace", "cli-workspace", "-f", "cli.yaml", "up", "-d"}, options(env))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workspace != "/work/current/cli-workspace" {
		t.Fatalf("workspace = %q", cfg.Workspace)
	}
	if !reflect.DeepEqual(cfg.ComposeCommand, []string{"/opt/policy compose", "compose"}) {
		t.Fatalf("compose command: %#v", cfg.ComposeCommand)
	}
	wantFiles := []string{"/work/current/env-a.yaml", "/work/current/env-b.yaml", "/work/current/cli.yaml"}
	if !reflect.DeepEqual(cfg.ComposeFiles, wantFiles) {
		t.Fatalf("compose files = %#v, want %#v", cfg.ComposeFiles, wantFiles)
	}
	if !reflect.DeepEqual(cfg.Command, []string{"up", "-d"}) {
		t.Fatalf("command = %#v", cfg.Command)
	}
	for key, want := range map[string]string{
		"workspace":       "cli",
		"compose_command": "environment",
		"compose_files":   "environment+cli",
		"state":           "environment",
	} {
		if cfg.Sources[key] != want {
			t.Errorf("source %s = %q, want %q", key, cfg.Sources[key], want)
		}
	}
}

func TestParseMacOSGUIRejected(t *testing.T) {
	t.Parallel()
	_, err := Parse([]string{"--gui=x11"}, ParseOptions{CallerDir: "/work", HomeDir: "/Users/a", Platform: "darwin", Getenv: func(string) string { return "" }})
	if err == nil {
		t.Fatal("expected macOS GUI rejection")
	}

	cfg, err := Parse([]string{"--no-gui"}, ParseOptions{CallerDir: "/work", HomeDir: "/Users/a", Platform: "darwin", Getenv: func(string) string { return "" }})
	if err != nil || cfg.GUI.Mode != "none" {
		t.Fatalf("headless macOS parse = %#v, %v", cfg, err)
	}
}

func TestParseStateConflict(t *testing.T) {
	t.Parallel()
	_, err := Parse([]string{"--private-env", "--state-volume", "custom"}, options(nil))
	if err == nil {
		t.Fatal("expected state conflict")
	}
}

func TestParseInvalidComposeCommand(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "[]", `["docker",""]`, `"docker compose"`, `[1]`} {
		env := map[string]string{"HCORRAL_COMPOSE_COMMAND": value}
		if value == "" {
			env["HCORRAL_COMPOSE_COMMAND"] = "[]"
		}
		if _, err := Parse(nil, options(env)); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestParseValidatesSessionTarget(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"bad:window", "bad session", "-", string(make([]byte, 65))} {
		if value == "-" {
			if _, err := Parse(nil, options(map[string]string{"HCORRAL_BYOBU_SESSION": value})); err != nil {
				t.Fatalf("safe punctuation session %q rejected: %v", value, err)
			}
			continue
		}
		if _, err := Parse(nil, options(map[string]string{"HCORRAL_BYOBU_SESSION": value})); err == nil {
			t.Errorf("unsafe session %q accepted", value)
		}
	}
}

func options(env map[string]string) ParseOptions {
	return ParseOptions{
		CallerDir: "/work/current",
		HomeDir:   "/home/alice",
		Platform:  "linux",
		Getenv: func(key string) string {
			return env[key]
		},
	}
}
