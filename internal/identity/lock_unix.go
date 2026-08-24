//go:build linux || darwin

package identity

import (
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
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create mutation lock directory: %w", err)
	}
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("mutation lock directory is not a physical directory: %s", directory)
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
	if runtime.GOOS == "darwin" {
		root := os.Getenv("TMPDIR")
		if root == "" {
			root = os.TempDir()
		}
		return filepath.Join(root, fmt.Sprintf("hcorral-%d", os.Getuid()), "locks", project+".lock"), nil
	}
	if root := os.Getenv("XDG_RUNTIME_DIR"); root != "" {
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("XDG_RUNTIME_DIR must be absolute")
		}
		return filepath.Join(root, "hcorral", "locks", project+".lock"), nil
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
	return filepath.Join(root, "hcorral", "locks", project+".lock"), nil
}
