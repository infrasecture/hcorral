package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

func TestSlug(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"payment-api":                          "payment_api",
		"Client Portal":                        "client_portal",
		"___":                                  "workspace",
		"A---B___C":                            "a_b_c",
		"café":                                 "caf",
		"abcdefghijklmnopqrstuvwxyz1234567890": "abcdefghijklmnopqrstuvwxyz123456",
	}
	for input, want := range tests {
		if got := Slug(input); got != want {
			t.Errorf("Slug(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestResolveGoldenVectors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		path        string
		slug        string
		workspaceID string
		corralID    string
		project     string
	}{
		{
			path:        "/home/alice/git/payment-api",
			slug:        "payment_api",
			workspaceID: "4324ea28e1f1b16d05a1a0cd94666b6a800e11dc9fbd0c193ca1e5fcd012309e",
			corralID:    "43d4c0416682227314946421f56ea06c77f7d9ca7e570a5d3e7f2e388326c47b",
			project:     "hcorral-payment_api-43d4c04",
		},
		{
			path:        "/home/alice/Work/Client Portal",
			slug:        "client_portal",
			workspaceID: "2ac50aae6196ec2a039ab1d828c218f0bfb40486965e03682b73252274154c98",
			corralID:    "666cedef4a8067b91eaba1ddc903b367a1f6c2fb51b71c4d179191a1788a2539",
			project:     "hcorral-client_portal-666cede",
		},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			// Resolve requires the path to exist. HashPath mirrors Resolve's
			// stable calculation so the specification vectors stay host-neutral.
			workspaceID := hashPath(test.path)
			if workspaceID != test.workspaceID {
				t.Fatalf("workspace ID = %s, want %s", workspaceID, test.workspaceID)
			}
			corralID := newCorralHash(test.path, "codex")
			if corralID != test.corralID {
				t.Fatalf("corral ID = %s, want %s", corralID, test.corralID)
			}
			if got := "hcorral-" + test.slug + "-" + corralID[:7]; got != test.project {
				t.Fatalf("project = %s, want %s", got, test.project)
			}
			if got := strings.Count(test.project, "-"); got != 2 {
				t.Fatalf("generated project %q has %d hyphens, want exactly 2", test.project, got)
			}
		})
	}
}

func TestResolvePhysicalSymlinkAndOverride(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	workspace := filepath.Join(root, "Payment API")
	if err := os.Mkdir(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(workspace, link); err != nil {
		t.Fatal(err)
	}

	got, err := Resolve(root, "link", "codex", "hcorral-payments")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != workspace || got.Slug != "payment_api" || got.Project != "hcorral-payments" || got.Generated {
		t.Fatalf("unexpected identity: %#v", got)
	}
}

func TestValidateProject(t *testing.T) {
	t.Parallel()
	for _, invalid := range []string{"", "Upper", "-leading", "has.dot", string(make([]byte, 64))} {
		if err := ValidateProject(invalid); err == nil {
			t.Errorf("ValidateProject(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestHarnessChangesCorralButNotWorkspaceIdentity(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	codex, err := Resolve(root, root, "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	claude, err := Resolve(root, root, "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	if codex.FullID != claude.FullID || codex.ShortID != claude.ShortID {
		t.Fatalf("workspace identity changed with harness: %#v %#v", codex, claude)
	}
	if codex.CorralID == claude.CorralID || codex.Project == claude.Project {
		t.Fatalf("harnesses did not get independent corrals: %#v %#v", codex, claude)
	}
	if WorkspaceVolumeName(codex) != WorkspaceVolumeName(claude) {
		t.Fatal("same workspace did not share its private-state name")
	}
}

func TestCorralGoldenVectorsForBuiltInHarnesses(t *testing.T) {
	t.Parallel()
	path := "/home/alice/git/payment-api"
	want := map[string]string{
		"codex":  "43d4c0416682227314946421f56ea06c77f7d9ca7e570a5d3e7f2e388326c47b",
		"claude": "df2852801540d3695b396032c8ef85e5584b52d4deb21f248fc861d9d2e82bca",
		"pi":     "439a70c5254b5490e79f2ca14dcee549bb1e9861aa2a60580cfe8f9d70f37f5f",
	}
	for harness, expected := range want {
		if got := newCorralHash(path, harness); got != expected {
			t.Errorf("%s corral ID = %s, want %s", harness, got, expected)
		}
	}
}

func TestVerifyContainerRejectsForcedSevenCharacterCollision(t *testing.T) {
	t.Parallel()
	workspace := Workspace{Project: "hcorral-demo-2ac50aa", FullID: "2ac50aa" + strings.Repeat("1", 57)}
	container := containerruntime.Container{Name: "/" + workspace.Project}
	container.Config.Labels = map[string]string{
		LabelWorkspaceID: "2ac50aa" + strings.Repeat("2", 57), LabelWorkspaceScheme: WorkspaceSchemeVersion, LabelRuntimeSchema: RuntimeSchemaVersion,
	}
	if err := VerifyContainer(&container, workspace); err == nil {
		t.Fatal("equal seven-character suffix with different full ID was accepted")
	}
}

func hashPath(path string) string {
	// Kept in the test package through a temporary physical directory would
	// make the golden paths host-dependent; exercise the exact namespace bytes.
	h := newWorkspaceHash(path)
	return h
}
