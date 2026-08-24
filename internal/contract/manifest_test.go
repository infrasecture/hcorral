package contract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

type manifest struct {
	Schema   int `json:"schema"`
	Baseline struct {
		Project    string `json:"project"`
		Commit     string `json:"commit"`
		Inspected  string `json:"inspected"`
		Provenance string `json:"provenance"`
	} `json:"baseline"`
	Features []feature `json:"features"`
}

type feature struct {
	ID                   string   `json:"id"`
	Requirement          string   `json:"requirement"`
	BaselineEvidence     string   `json:"baseline_evidence"`
	ImplementationChange string   `json:"implementation_change"`
	Platforms            []string `json:"platforms"`
	AutomatedEvidence    []string `json:"automated_evidence"`
	ManualEvidence       []string `json:"manual_evidence"`
	Status               string   `json:"status"`
	IntentionalDelta     string   `json:"intentional_delta"`
}

func TestFeatureParityManifestIsCompleteAndEvidenced(t *testing.T) {
	t.Parallel()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
	content, err := os.ReadFile(filepath.Join(root, "tests/contract/feature-parity.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var got manifest
	if err := json.Unmarshal(content, &got); err != nil {
		t.Fatalf("feature-parity.yaml must remain JSON-compatible YAML: %v", err)
	}
	if got.Schema != 1 || got.Baseline.Project != "myCodex" || len(got.Baseline.Commit) != 40 || got.Baseline.Inspected == "" {
		t.Fatalf("invalid baseline: %#v", got.Baseline)
	}
	assertEvidencePath(t, root, got.Baseline.Provenance)

	required := []string{"CLI-001", "COMPOSE-001", "GUI-001", "IMAGE-PUBLISH-001", "IMAGE-TOOLS-001", "IMAGE-USER-001", "INFO-001", "LEGACY-001", "LIFE-001", "PATH-001", "RELEASE-001", "SESSION-001", "STATE-001", "UPDATE-001"}
	seen := make(map[string]bool, len(got.Features))
	for _, row := range got.Features {
		if row.ID == "" || seen[row.ID] {
			t.Fatalf("missing or duplicate feature ID %q", row.ID)
		}
		seen[row.ID] = true
		if row.Requirement == "" || row.BaselineEvidence == "" || row.ImplementationChange == "" || row.IntentionalDelta == "" {
			t.Errorf("feature %s has incomplete traceability fields", row.ID)
		}
		if row.Status != "implemented" || len(row.Platforms) == 0 || len(row.AutomatedEvidence) == 0 {
			t.Errorf("feature %s is not implemented and automatically evidenced", row.ID)
		}
		for _, path := range row.AutomatedEvidence {
			assertEvidencePath(t, root, path)
		}
	}
	actual := make([]string, 0, len(seen))
	for id := range seen {
		actual = append(actual, id)
	}
	sort.Strings(actual)
	if len(actual) != len(required) {
		t.Fatalf("feature IDs = %v, want %v", actual, required)
	}
	for index := range required {
		if actual[index] != required[index] {
			t.Fatalf("feature IDs = %v, want %v", actual, required)
		}
	}
}

func assertEvidencePath(t *testing.T, root, relative string) {
	t.Helper()
	if relative == "" || filepath.IsAbs(relative) || filepath.Clean(relative) != relative {
		t.Errorf("invalid evidence path %q", relative)
		return
	}
	info, err := os.Stat(filepath.Join(root, relative))
	if err != nil {
		t.Errorf("evidence %q is unavailable: %v", relative, err)
		return
	}
	if info.IsDir() {
		t.Errorf("evidence %q must identify a concrete file", relative)
	}
}
