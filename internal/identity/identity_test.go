package identity

import (
	"os"
	"path/filepath"
	"testing"
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
		path    string
		slug    string
		fullID  string
		project string
	}{
		{
			path:    "/home/alice/git/payment-api",
			slug:    "payment_api",
			fullID:  "4324ea28e1f1b16d05a1a0cd94666b6a800e11dc9fbd0c193ca1e5fcd012309e",
			project: "hcorral-payment_api-4324ea2",
		},
		{
			path:    "/home/alice/Work/Client Portal",
			slug:    "client_portal",
			fullID:  "2ac50aae6196ec2a039ab1d828c218f0bfb40486965e03682b73252274154c98",
			project: "hcorral-client_portal-2ac50aa",
		},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			// Resolve requires the path to exist. HashPath mirrors Resolve's
			// stable calculation so the specification vectors stay host-neutral.
			fullID := hashPath(test.path)
			if fullID != test.fullID {
				t.Fatalf("full ID = %s, want %s", fullID, test.fullID)
			}
			if got := "hcorral-" + test.slug + "-" + fullID[:7]; got != test.project {
				t.Fatalf("project = %s, want %s", got, test.project)
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

	got, err := Resolve(root, "link", "hcorral-payments")
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

func hashPath(path string) string {
	// Kept in the test package through a temporary physical directory would
	// make the golden paths host-dependent; exercise the exact namespace bytes.
	h := newWorkspaceHash(path)
	return h
}
