package compose

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/infrasecture/hcorral/internal/config"
)

type GeneratedFile struct{ Path string }

func (f GeneratedFile) Cleanup() error {
	if f.Path == "" {
		return nil
	}
	return os.Remove(f.Path)
}

func ExtraMountOverlay(cfg config.Config) (GeneratedFile, error) {
	if len(cfg.ExtraVolumes) == 0 {
		return GeneratedFile{}, nil
	}
	volumes := make([]string, 0, len(cfg.ExtraVolumes))
	topLevelVolumes := map[string]any{}
	for _, specification := range cfg.ExtraVolumes {
		normalized, target, err := normalizeMount(cfg.CallerDir, specification)
		if err != nil {
			return GeneratedFile{}, err
		}
		_ = target
		parts := strings.Split(normalized, ":")
		if filepath.IsAbs(parts[0]) {
			volumes = append(volumes, normalized)
		} else {
			logical := extraVolumeLogicalName(parts[0])
			parts[0] = logical
			volumes = append(volumes, strings.Join(parts, ":"))
			topLevelVolumes[logical] = map[string]any{"external": true, "name": strings.Split(normalized, ":")[0]}
		}
	}
	document := map[string]any{"services": map[string]any{"hcorral": map[string]any{"volumes": volumes}}}
	if len(topLevelVolumes) > 0 {
		document["volumes"] = topLevelVolumes
	}
	content, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return GeneratedFile{}, err
	}
	cache, err := generatedDirectory()
	if err != nil {
		return GeneratedFile{}, err
	}
	file, err := os.CreateTemp(cache, "mounts.*.json")
	if err != nil {
		return GeneratedFile{}, fmt.Errorf("create mount overlay: %w", err)
	}
	path := file.Name()
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		os.Remove(path)
		return GeneratedFile{}, err
	}
	if _, err := file.Write(content); err != nil {
		file.Close()
		os.Remove(path)
		return GeneratedFile{}, err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return GeneratedFile{}, err
	}
	return GeneratedFile{Path: path}, nil
}

func extraVolumeLogicalName(name string) string {
	digest := sha256.Sum256([]byte(name))
	return "hcorral_extra_" + fmt.Sprintf("%x", digest[:6])
}

func normalizeMount(caller, specification string) (string, string, error) {
	if specification == "" || strings.ContainsAny(specification, "\x00\r\n") {
		return "", "", errors.New("volume spec must be non-empty and single-line")
	}
	parts := strings.Split(specification, ":")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("volume spec must use source:target[:mode]: %q", specification)
	}
	source, target := parts[0], filepath.Clean(parts[1])
	if !filepath.IsAbs(target) {
		return "", "", fmt.Errorf("volume target must be absolute: %q", parts[1])
	}
	if source == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", err
		}
		source = home
	} else if strings.HasPrefix(source, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", err
		}
		source = filepath.Join(home, source[2:])
	} else if source == "." || source == ".." || strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") {
		source = filepath.Clean(filepath.Join(caller, source))
	} else if !filepath.IsAbs(source) && strings.ContainsRune(source, '/') {
		source = filepath.Clean(filepath.Join(caller, source))
	}
	normalized := source + ":" + target
	if len(parts) == 3 {
		if parts[2] == "" {
			return "", "", errors.New("volume mode must not be empty")
		}
		normalized += ":" + parts[2]
	}
	return normalized, target, nil
}

func generatedDirectory() (string, error) {
	root := os.Getenv("XDG_CACHE_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(home, ".cache")
	}
	if !filepath.IsAbs(root) {
		return "", errors.New("XDG_CACHE_HOME must be absolute")
	}
	directory := filepath.Join(root, "hcorral", "generated")
	if err := secureMkdirAll(directory); err != nil {
		return "", err
	}
	return directory, nil
}
