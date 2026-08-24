package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	workspaceNamespace = "ai.infrasecture.hcorral.workspace.v1"
	corralNamespace    = "ai.infrasecture.hcorral.corral.v1"
)

var projectPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

// Workspace is the stable identity derived from a physical workspace path.
// Project is either the generated Compose project name or an explicitly
// validated override. FullID, never ShortID or Slug, proves ownership.
type Workspace struct {
	Path          string `json:"path"`
	Base          string `json:"basename"`
	Slug          string `json:"slug"`
	FullID        string `json:"workspace_id"`
	ShortID       string `json:"workspace_short_id"`
	CorralID      string `json:"corral_id"`
	CorralShortID string `json:"corral_short_id"`
	Harness       string `json:"harness"`
	Project       string `json:"project"`
	Generated     bool   `json:"generated_project"`
}

// Resolve derives the workspace and corral identities for one explicit
// canonical harness selection and optional Compose project override.
func Resolve(callerDir, selectedPath, harness, projectOverride string) (Workspace, error) {
	if harness == "" {
		return Workspace{}, errors.New("canonical harness type is empty")
	}
	path, err := resolvePhysicalPath(callerDir, selectedPath)
	if err != nil {
		return Workspace{}, err
	}

	base := filepath.Base(path)
	slug := Slug(base)
	hash := sha256.New()
	_, _ = hash.Write([]byte(workspaceNamespace))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write([]byte(path))
	fullID := hex.EncodeToString(hash.Sum(nil))
	corralHash := sha256.New()
	_, _ = corralHash.Write([]byte(corralNamespace))
	_, _ = corralHash.Write([]byte{0})
	_, _ = corralHash.Write([]byte(path))
	_, _ = corralHash.Write([]byte{0})
	_, _ = corralHash.Write([]byte(harness))
	corralID := hex.EncodeToString(corralHash.Sum(nil))

	project := "hcorral-" + slug + "-" + corralID[:7]
	generated := true
	if projectOverride != "" {
		project = projectOverride
		generated = false
	}
	if err := ValidateProject(project); err != nil {
		return Workspace{}, err
	}

	return Workspace{
		Path:          path,
		Base:          base,
		Slug:          slug,
		FullID:        fullID,
		ShortID:       fullID[:7],
		CorralID:      corralID,
		CorralShortID: corralID[:7],
		Harness:       harness,
		Project:       project,
		Generated:     generated,
	}, nil
}

func resolvePhysicalPath(callerDir, selectedPath string) (string, error) {
	if callerDir == "" {
		return "", errors.New("caller directory is empty")
	}
	if selectedPath == "" {
		selectedPath = callerDir
	} else if !filepath.IsAbs(selectedPath) {
		selectedPath = filepath.Join(callerDir, selectedPath)
	}

	abs, err := filepath.Abs(selectedPath)
	if err != nil {
		return "", fmt.Errorf("resolve workspace absolute path: %w", err)
	}
	physical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve physical workspace %q: %w", selectedPath, err)
	}
	physical = filepath.Clean(physical)
	info, err := os.Stat(physical)
	if err != nil {
		return "", fmt.Errorf("inspect workspace %q: %w", physical, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace %q is not a directory", physical)
	}
	return physical, nil
}

// Slug converts a basename to the readable part of a generated project name.
// Hyphens are deliberately excluded so generated project names always have
// exactly the two structural hyphens in hcorral-<slug>-<7-hex>.
func Slug(base string) string {
	var b strings.Builder
	underscore := false
	for _, r := range base {
		switch {
		case r >= 'A' && r <= 'Z':
			b.WriteByte(byte(r - 'A' + 'a'))
			underscore = false
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			underscore = false
		default:
			if b.Len() > 0 && !underscore {
				b.WriteByte('_')
				underscore = true
			}
		}
	}

	slug := strings.Trim(b.String(), "_")
	if len(slug) > 32 {
		slug = strings.TrimRight(slug[:32], "_")
	}
	if slug == "" {
		return "workspace"
	}
	return slug
}

func ValidateProject(project string) error {
	if !projectPattern.MatchString(project) {
		return fmt.Errorf("invalid project name %q: must match [a-z0-9][a-z0-9_-]{0,62}", project)
	}
	return nil
}

func WorkspaceNamespace() string { return workspaceNamespace }
func CorralNamespace() string    { return corralNamespace }
