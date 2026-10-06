// Package session handles Codex's persisted conversation format. It does not
// select Docker resources or perform workstation lifecycle operations.
package session

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// Home pins the selected directory. The root may be a symlink when opened;
// descendants are always opened relative to directory descriptors, without
// following symlinks. A concurrent directory replacement cannot escape Home.
type Home struct {
	Path        string
	logicalPath string
	dir         *os.File
}

func OpenHome(path string) (*Home, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	logicalPath := abs
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve Codex home: %w", err)
	}
	fd, err := unix.Open(abs, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open Codex home: %w", err)
	}
	return &Home{Path: abs, logicalPath: logicalPath, dir: os.NewFile(uintptr(fd), abs)}, nil
}

// ResolveHostHome uses the Docker client's path namespace. It only resolves
// precedence and relative paths; it does not create or initialize a home.
func ResolveHostHome(explicit, codexHome, userHome, callerDir string) (string, error) {
	path := explicit
	if path == "" {
		path = codexHome
	}
	if path == "" {
		if userHome == "" {
			return "", fmt.Errorf("HOME is required when no host Codex home is specified")
		}
		path = filepath.Join(userHome, ".codex")
	}
	if !filepath.IsAbs(path) {
		if !filepath.IsAbs(callerDir) {
			return "", fmt.Errorf("caller directory must be absolute")
		}
		path = filepath.Join(callerDir, path)
	}
	return filepath.Clean(path), nil
}

func (h *Home) Close() error { return h.dir.Close() }

func validRelative(name string) bool {
	return name != "." && filepath.IsLocal(name) && filepath.Clean(name) == name &&
		!strings.ContainsAny(name, "\\\x00")
}

// open also uses O_NONBLOCK: a malicious FIFO must be rejected, not hang
// before the caller gets an opportunity to check its file type.
func (h *Home) open(name string, flags int, mode uint32) (*os.File, error) {
	if !validRelative(name) {
		return nil, fmt.Errorf("invalid path within Codex home: %q", name)
	}
	parts := strings.Split(name, string(filepath.Separator))
	parent := h.dir
	for _, component := range parts[:len(parts)-1] {
		fd, err := unix.Openat(int(parent.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if parent != h.dir {
			parent.Close()
		}
		if err != nil {
			return nil, &os.PathError{Op: "open directory", Path: name, Err: err}
		}
		parent = os.NewFile(uintptr(fd), component)
	}
	if parent != h.dir {
		defer parent.Close()
	}
	fd, err := unix.Openat(int(parent.Fd()), parts[len(parts)-1], flags|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, mode)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(fd), name), nil
}

func (h *Home) regular(name string) (*os.File, error) {
	f, err := h.open(name, unix.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		if err == nil {
			err = fmt.Errorf("not a regular file")
		}
		return nil, fmt.Errorf("read %s: %w", name, err)
	}
	return f, nil
}

// inventory examines names only; it never reads unrelated conversations.
func (h *Home) inventory(ctx context.Context) ([]candidate, error) {
	var found []candidate
	stack := []string{"sessions", "archived_sessions"}
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		dir, err := h.open(name, unix.O_RDONLY|unix.O_DIRECTORY, 0)
		if os.IsNotExist(err) && (name == "sessions" || name == "archived_sessions") {
			continue
		}
		if err != nil {
			return nil, err
		}
		err = func() error {
			defer dir.Close()
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				entries, err := dir.ReadDir(128)
				if err != nil && err != io.EOF {
					return err
				}
				for _, entry := range entries {
					path := filepath.Join(name, entry.Name())
					if entry.Type()&os.ModeSymlink != 0 {
						return fmt.Errorf("symlink in session storage: %s", path)
					}
					if entry.IsDir() {
						stack = append(stack, path)
						continue
					}
					if c, ok := parseName(path); ok {
						found = append(found, c)
					}
				}
				if err == io.EOF {
					return nil
				}
			}
		}()
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].path < found[j].path })
	return found, nil
}
