package compose

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

//go:embed assets/v1/*.yaml
var builtins embed.FS

type AssetPaths struct {
	Base    string
	X11     string
	Wayland string
}

type Materializer struct {
	CacheHome string
	Platform  string
}

func (m Materializer) Materialize() (AssetPaths, error) {
	root, err := m.cacheRoot()
	if err != nil {
		return AssetPaths{}, err
	}
	paths := AssetPaths{}
	for name, target := range map[string]*string{
		"compose.yaml":     &paths.Base,
		"gui-x11.yaml":     &paths.X11,
		"gui-wayland.yaml": &paths.Wayland,
	} {
		content, readErr := builtins.ReadFile("assets/v1/" + name)
		if readErr != nil {
			return AssetPaths{}, fmt.Errorf("read built-in Compose asset %s: %w", name, readErr)
		}
		path, writeErr := materializeOne(root, name, content)
		if writeErr != nil {
			return AssetPaths{}, writeErr
		}
		*target = path
	}
	return paths, nil
}

func (m Materializer) cacheRoot() (string, error) {
	platform := m.Platform
	if platform == "" {
		platform = runtime.GOOS
	}
	if m.CacheHome != "" {
		if !filepath.IsAbs(m.CacheHome) {
			return "", errors.New("Compose cache home must be absolute")
		}
		return filepath.Join(m.CacheHome, "hcorral", "compose", "v1"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home for Compose cache: %w", err)
	}
	if platform == "darwin" {
		return filepath.Join(home, "Library", "Caches", "hcorral", "compose", "v1"), nil
	}
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		if !filepath.IsAbs(xdg) {
			return "", errors.New("XDG_CACHE_HOME must be absolute")
		}
		return filepath.Join(xdg, "hcorral", "compose", "v1"), nil
	}
	return filepath.Join(home, ".cache", "hcorral", "compose", "v1"), nil
}

func materializeOne(root, name string, content []byte) (string, error) {
	digest := sha256.Sum256(content)
	directory := filepath.Join(root, hex.EncodeToString(digest[:]))
	if err := secureMkdirAll(directory); err != nil {
		return "", err
	}
	path := filepath.Join(directory, name)
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("cached Compose asset is not a physical regular file: %s", path)
		}
		existing, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("inspect cached Compose asset: %w", err)
		}
		if string(existing) != string(content) {
			return "", fmt.Errorf("cached Compose asset has unexpected content: %s", path)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return "", fmt.Errorf("protect cached Compose asset: %w", err)
		}
		return path, nil
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return "", fmt.Errorf("inspect cached Compose asset: %w", statErr)
	}

	temporary, err := os.CreateTemp(directory, "."+name+".*")
	if err != nil {
		return "", fmt.Errorf("create temporary Compose asset: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return "", fmt.Errorf("protect temporary Compose asset: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		temporary.Close()
		return "", fmt.Errorf("write temporary Compose asset: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return "", fmt.Errorf("sync temporary Compose asset: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close temporary Compose asset: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		if existing, readErr := os.ReadFile(path); readErr == nil && string(existing) == string(content) {
			return path, nil
		}
		return "", fmt.Errorf("install cached Compose asset: %w", err)
	}
	return path, nil
}

func secureMkdirAll(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create Compose cache: %w", err)
	}
	current := path
	for {
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect Compose cache component: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("Compose cache component is not a physical directory: %s", current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("protect Compose cache: %w", err)
	}
	return nil
}
