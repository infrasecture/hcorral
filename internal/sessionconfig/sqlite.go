// Package sessionconfig resolves the persisted configuration needed to locate
// Codex metadata. It does not start Codex, load credentials or evaluate policy.
package sessionconfig

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"golang.org/x/sys/unix"
)

const MaximumConfigBytes = 8 << 20

type ReadFile func(context.Context, string) ([]byte, error)

type SQLiteOptions struct {
	Home, CWD   string
	Environment string
	Explicit    string
}

type Resolution struct {
	Path   string `json:"path"`
	Source string `json:"source"`
}

// SQLiteHome handles the file-based Unix base configuration, local requirements
// and environment contract researched at Codex 0.160.0. Runtime flags, selected
// profile-v2 files, cloud configuration and macOS managed preferences require an
// explicit endpoint path when they change sqlite_home. No complete replication
// of Codex's configuration or project-trust engine is claimed here.
func SQLiteHome(ctx context.Context, options SQLiteOptions, read ReadFile) (Resolution, error) {
	if !path.IsAbs(options.Home) || !path.IsAbs(options.CWD) {
		return Resolution{}, errors.New("Codex home and configuration working directory must be absolute")
	}
	if err := ctx.Err(); err != nil {
		return Resolution{}, err
	}
	if options.Explicit != "" {
		if !path.IsAbs(options.Explicit) {
			return Resolution{}, errors.New("explicit SQLite home must be resolved to an absolute path")
		}
		return resolved(options.Explicit, "/", "explicit option")
	}
	if read == nil {
		return Resolution{}, errors.New("configuration reader is required")
	}
	result := Resolution{Path: path.Clean(options.Home), Source: "Codex home default"}
	if value := strings.TrimSpace(options.Environment); value != "" {
		var err error
		result, err = resolved(value, options.CWD, "CODEX_SQLITE_HOME")
		if err != nil {
			return Resolution{}, err
		}
	}
	for _, name := range []string{"/etc/codex/config.toml", path.Join(options.Home, "config.toml")} {
		value, err := sqliteSetting(ctx, read, name)
		if err != nil {
			return Resolution{}, err
		}
		if value != nil {
			result, err = resolved(*value, path.Dir(name), name+":sqlite_home")
			if err != nil {
				return Resolution{}, err
			}
		}
	}
	// Requirements constrain the final value. Legacy managed config has higher
	// precedence than both the system requirements and ordinary config layers.
	managed := false
	for _, name := range []string{"/etc/codex/requirements.toml", "/etc/codex/managed_config.toml"} {
		value, err := sqliteSetting(ctx, read, name)
		if err != nil {
			return Resolution{}, err
		}
		if value != nil {
			result, err = resolved(*value, path.Dir(name), name+":sqlite_home")
			if err != nil {
				return Resolution{}, err
			}
			managed = true
		}
	}
	if !managed {
		// A project file can override sqlite_home only when enabled by Codex's
		// trust/project-root rules. Inspect candidates but do not approximate
		// those rules or silently use the base database when a candidate exists.
		for dir := path.Clean(options.CWD); ; dir = path.Dir(dir) {
			name := path.Join(dir, ".codex", "config.toml")
			if name != path.Join(options.Home, "config.toml") {
				value, err := sqliteSetting(ctx, read, name)
				if err != nil {
					return Resolution{}, err
				}
				if value != nil {
					return Resolution{}, fmt.Errorf("project configuration %q declares sqlite_home; specify the effective SQLite home explicitly because project trust and runtime overrides are not inferred", name)
				}
			}
			if dir == "/" {
				break
			}
		}
	}
	return result, nil
}

func resolved(value, base, source string) (Resolution, error) {
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return Resolution{}, fmt.Errorf("invalid SQLite directory in %s", source)
	}
	if !path.IsAbs(value) {
		value = path.Join(base, value)
	}
	return Resolution{Path: path.Clean(value), Source: source}, nil
}

func sqliteSetting(ctx context.Context, read ReadFile, name string) (*string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := read(ctx, name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Codex configuration %q: %w", name, err)
	}
	if len(data) > MaximumConfigBytes {
		return nil, fmt.Errorf("Codex configuration %q exceeds %d bytes", name, MaximumConfigBytes)
	}
	var config struct {
		SQLiteHome *string `toml:"sqlite_home"`
	}
	if err := toml.Unmarshal(data, &config); err != nil {
		// Parser diagnostics can include source snippets containing credentials
		// for other tools. Report the file without echoing its contents.
		return nil, fmt.Errorf("cannot parse sqlite_home from Codex configuration %q; correct the TOML or supply an explicit SQLite home", name)
	}
	return config.SQLiteHome, nil
}

// ReadLocalFile follows intentional configuration symlinks as Codex does, but
// rejects special files and bounds reads. O_NONBLOCK prevents a FIFO from
// hanging before its type can be inspected. No contents are logged.
func ReadLocalFile(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fd, err := unix.Open(name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open config", Path: name, Err: err}
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > MaximumConfigBytes {
		return nil, errors.New("Codex configuration must be a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, MaximumConfigBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaximumConfigBytes {
		return nil, errors.New("Codex configuration exceeds its size limit")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}
