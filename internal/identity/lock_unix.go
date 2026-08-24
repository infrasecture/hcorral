//go:build linux || darwin

package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

type Lock struct {
	file *os.File
	Path string
}

func AcquireLock(project string) (*Lock, error) {
	path, err := lockPath(project)
	if err != nil {
		return nil, err
	}
	return acquireLockPath(path)
}

// AcquireVolumeLock serializes launcher-managed volume creation, reference
// checks, and removal without placing a user-controlled Docker volume name in
// the filesystem. The complete SHA-256 keeps the lock key collision-resistant.
func AcquireVolumeLock(volume string) (*Lock, error) {
	if volume == "" {
		return nil, fmt.Errorf("volume lock name is empty")
	}
	digest := sha256.Sum256([]byte(volume))
	root, err := lockRoot()
	if err != nil {
		return nil, err
	}
	return acquireLockPath(filepath.Join(root, "volumes", hex.EncodeToString(digest[:])+".lock"))
}

func acquireLockPath(path string) (*Lock, error) {
	directory := filepath.Dir(path)
	base := filepath.Dir(filepath.Dir(directory))
	if err := secureLockDirectory(base, filepath.Base(filepath.Dir(directory)), filepath.Base(directory)); err != nil {
		return nil, fmt.Errorf("create mutation lock directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, fmt.Errorf("protect mutation lock directory: %w", err)
	}
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open mutation lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		file.Close()
		return nil, fmt.Errorf("acquire mutation lock: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		syscall.Flock(fd, syscall.LOCK_UN)
		file.Close()
		return nil, fmt.Errorf("protect mutation lock: %w", err)
	}
	return &Lock{file: file, Path: path}, nil
}

func secureLockDirectory(base string, components ...string) error {
	info, err := os.Lstat(base)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(base, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(base)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("lock root is not a physical directory: %s", base)
	}
	current := base
	for _, component := range components {
		current = filepath.Join(current, component)
		if err := os.Mkdir(current, 0o700); err != nil && !os.IsExist(err) {
			return err
		}
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("lock path is not a physical directory: %s", current)
		}
	}
	return nil
}

func (l *Lock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}

func lockPath(project string) (string, error) {
	if err := ValidateProject(project); err != nil {
		return "", err
	}
	root, err := lockRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, project+".lock"), nil
}

func lockRoot() (string, error) {
	if runtime.GOOS == "darwin" {
		root := os.Getenv("TMPDIR")
		if root == "" {
			root = os.TempDir()
		}
		return filepath.Join(root, fmt.Sprintf("hcorral-%d", os.Getuid()), "locks"), nil
	}
	if root := os.Getenv("XDG_RUNTIME_DIR"); root != "" {
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("XDG_RUNTIME_DIR must be absolute")
		}
		return filepath.Join(root, "hcorral", "locks"), nil
	}
	root := os.Getenv("XDG_CACHE_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".cache")
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("XDG_CACHE_HOME must be absolute")
	}
	return filepath.Join(root, "hcorral", "locks"), nil
}
