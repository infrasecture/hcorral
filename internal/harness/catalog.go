package harness

import "strings"

// Definition describes a built-in harness image stream. Image versions are
// independent from launcher versions; latest is the moving default for each
// stream, while release tags remain available for explicit pinning.
type Definition struct {
	Type         string
	DefaultImage string
	Command      string
	VersionLabel string
}

var builtins = map[string]Definition{
	"codex": {
		Type:         "codex",
		DefaultImage: "ghcr.io/infrasecture/hcorral-codex:latest",
		Command:      "codex",
		VersionLabel: "ai.infrasecture.hcorral.codex.version",
	},
	"claude": {
		Type:         "claude",
		DefaultImage: "ghcr.io/infrasecture/hcorral-claude:latest",
		Command:      "claude",
		VersionLabel: "ai.infrasecture.hcorral.claude.version",
	},
	"pi": {
		Type:         "pi",
		DefaultImage: "ghcr.io/infrasecture/hcorral-pi:latest",
		Command:      "pi",
		VersionLabel: "ai.infrasecture.hcorral.pi.version",
	},
}

func Normalize(value string) string { return strings.ToLower(strings.TrimSpace(value)) }

func Lookup(value string) (Definition, bool) {
	definition, ok := builtins[Normalize(value)]
	return definition, ok
}

func DefaultImage(value string) (string, bool) {
	definition, ok := Lookup(value)
	return definition.DefaultImage, ok
}

func IsBuiltIn(value string) bool {
	_, ok := Lookup(value)
	return ok
}
