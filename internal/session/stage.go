package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// directory creates only missing components, with private permissions. Existing
// directories retain their ownership and modes; symlinks are never followed.
func (h *Home) directory(name string, create bool) (*os.File, error) {
	if !validRelative(name) {
		return nil, fmt.Errorf("invalid directory within Codex home: %q", name)
	}
	parent := h.dir
	for _, component := range strings.Split(name, string(filepath.Separator)) {
		if create {
			err := unix.Mkdirat(int(parent.Fd()), component, 0o700)
			if err == nil {
				// Persist each new directory's entry in its parent, not only
				// the eventual rollout entry in the innermost directory.
				err = parent.Sync()
			}
			if err != nil && !errors.Is(err, unix.EEXIST) {
				if parent != h.dir {
					parent.Close()
				}
				return nil, fmt.Errorf("create %s: %w", name, err)
			}
		}
		fd, err := unix.Openat(int(parent.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if parent != h.dir {
			parent.Close()
		}
		if err != nil {
			return nil, fmt.Errorf("open directory %s: %w", name, err)
		}
		parent = os.NewFile(uintptr(fd), component)
	}
	return parent, nil
}

func (h *Home) staging() (*Home, string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, "", err
	}
	name := ".hcorral-transfer-" + hex.EncodeToString(nonce[:])
	if err := unix.Mkdirat(int(h.dir.Fd()), name, 0o700); err != nil {
		return nil, "", err
	}
	dir, err := h.directory(name, false)
	if err != nil {
		unix.Unlinkat(int(h.dir.Fd()), name, unix.AT_REMOVEDIR)
		return nil, "", err
	}
	return &Home{Path: filepath.Join(h.Path, name), dir: dir}, name, nil
}

// Incoming owns private, verified staging files. Close removes only this
// transfer's staging area; it never removes installed or preexisting rollouts.
type Incoming struct {
	Plan        Plan
	home, stage *Home
	name        string
	slots       []string
	limits      Limits
}

func (in *Incoming) Close() error {
	if in.stage == nil {
		return nil
	}
	var result error
	for _, name := range in.slots {
		err := unix.Unlinkat(int(in.stage.dir.Fd()), name, 0)
		if !errors.Is(err, unix.ENOENT) {
			result = errors.Join(result, err)
		}
	}
	result = errors.Join(result, in.stage.Close())
	err := unix.Unlinkat(int(in.home.dir.Fd()), in.name, unix.AT_REMOVEDIR)
	if !errors.Is(err, unix.ENOENT) {
		result = errors.Join(result, err)
	}
	in.stage = nil
	return result
}
