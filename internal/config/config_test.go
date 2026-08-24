package config

import (
	"reflect"
	"testing"
)

func TestParseDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := Parse(nil, options(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Workspace != "/work/current" || cfg.ImageName != "ghcr.io/infrasecture/hcorral" || cfg.ImageTag != "latest" {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.StateMode != StateShared || cfg.GUI.Specified || !reflect.DeepEqual(cfg.ComposeCommand, []string{"docker", "compose"}) {
		t.Fatalf("unexpected typed defaults: %#v", cfg)
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
