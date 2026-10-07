package sessionconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSQLitePrecedenceAndRelativePathBases(t *testing.T) {
	for _, tc := range []struct {
		name, env, explicit, expected string
		files                         map[string]string
	}{
		{name: "default", expected: "/home/person/.codex"},
		{name: "environment", env: " state elsewhere \n", expected: "/workspace/sub/state elsewhere"},
		{name: "empty environment", env: " \n", expected: "/home/person/.codex"},
		{name: "system over environment", env: "/env", files: map[string]string{"/etc/codex/config.toml": "sqlite_home='system-state'"}, expected: "/etc/codex/system-state"},
		{name: "user over system", files: map[string]string{"/etc/codex/config.toml": "sqlite_home='/system'", "/home/person/.codex/config.toml": "sqlite_home='../state'"}, expected: "/home/person/state"},
		{name: "requirements", files: map[string]string{"/home/person/.codex/config.toml": "sqlite_home='/user'", "/etc/codex/requirements.toml": "sqlite_home='required'"}, expected: "/etc/codex/required"},
		{name: "legacy managed", files: map[string]string{"/etc/codex/requirements.toml": "sqlite_home='/required'", "/etc/codex/managed_config.toml": "sqlite_home='managed'", "/workspace/.codex/config.toml": "sqlite_home='/project'"}, expected: "/etc/codex/managed"},
		{name: "explicit", explicit: "/chosen database", files: map[string]string{"/home/person/.codex/config.toml": "broken = [ secret"}, expected: "/chosen database"},
		{name: "unrelated project config", files: map[string]string{"/workspace/.codex/config.toml": "model='some-model'"}, expected: "/home/person/.codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			read := func(_ context.Context, name string) ([]byte, error) {
				reads++
				if value, ok := tc.files[name]; ok {
					return []byte(value), nil
				}
				return nil, os.ErrNotExist
			}
			got, err := SQLiteHome(context.Background(), SQLiteOptions{Home: "/home/person/.codex", CWD: "/workspace/sub", Environment: tc.env, Explicit: tc.explicit}, read)
			if err != nil || got.Path != tc.expected || got.Source == "" {
				t.Fatalf("resolution: %+v %v", got, err)
			}
			if tc.explicit != "" && reads != 0 {
				t.Fatal("explicit storage selection consulted unrelated configuration")
			}
		})
	}
}

func TestSQLiteRefusesAmbiguousOrUnreadableConfigurationWithoutLeakingContents(t *testing.T) {
	for _, problem := range []string{"project", "syntax", "type", "empty", "permission", "cancelled", "oversized"} {
		t.Run(problem, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if problem == "cancelled" {
				cancel()
			}
			read := func(_ context.Context, name string) ([]byte, error) {
				if problem == "project" && name == "/workspace/.codex/config.toml" {
					return []byte("sqlite_home='/project'"), nil
				}
				if name != "/home/person/.codex/config.toml" {
					return nil, os.ErrNotExist
				}
				switch problem {
				case "syntax":
					return []byte("sqlite_home='TOP-SECRET\n"), nil
				case "type":
					return []byte("sqlite_home=['TOP-SECRET']"), nil
				case "empty":
					return []byte("sqlite_home=''"), nil
				case "permission":
					return nil, os.ErrPermission
				case "oversized":
					return []byte(strings.Repeat("#", MaximumConfigBytes+1)), nil
				default:
					return nil, os.ErrNotExist
				}
			}
			_, err := SQLiteHome(ctx, SQLiteOptions{Home: "/home/person/.codex", CWD: "/workspace/sub"}, read)
			if err == nil || strings.Contains(err.Error(), "TOP-SECRET") {
				t.Fatalf("unsafe config result: %v", err)
			}
			if problem == "permission" && !errors.Is(err, os.ErrPermission) {
				t.Fatal("lost permission error")
			}
		})
	}
}

func TestLocalConfigReadsSymlinksButRejectsSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(file, []byte("sqlite_home='state'"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked.toml")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if data, err := ReadLocalFile(context.Background(), link); err != nil || string(data) != "sqlite_home='state'" {
		t.Fatalf("symlink config: %q %v", data, err)
	}
	fifo := filepath.Join(dir, "pipe")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{dir, fifo} {
		if _, err := ReadLocalFile(context.Background(), name); err == nil {
			t.Fatal("accepted special config file")
		}
	}
}
