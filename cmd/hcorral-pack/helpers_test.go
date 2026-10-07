package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestHelperPackagingChecksBothArchitecturesAndIsReproducible(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte("package main\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	paths := make(map[string]string)
	for _, arch := range []string{"amd64", "arm64"} {
		paths[arch] = filepath.Join(dir, arch)
		cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-buildvcs=false", "-trimpath", "-o", paths[arch], source)
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch, "GOWORK=off")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("build fixture: %v\n%s", err, output)
		}
	}
	output := filepath.Join(dir, "payloads")
	args := []string{"-amd64", paths["amd64"], "-arm64", paths["arm64"], "-output", output}
	if err := helpers(args); err != nil {
		t.Fatal(err)
	}
	first := make(map[string][]byte)
	for arch, path := range paths {
		encoded, err := os.ReadFile(filepath.Join(output, "linux-"+arch+".gz"))
		if err != nil {
			t.Fatal(err)
		}
		first[arch] = encoded
		reader, err := gzip.NewReader(bytes.NewReader(encoded))
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := io.ReadAll(reader)
		reader.Close()
		original, readErr := os.ReadFile(path)
		if err != nil || readErr != nil || !bytes.Equal(decoded, original) {
			t.Fatal("payload is not the inspected executable")
		}
	}
	if err := helpers(args); err != nil {
		t.Fatal(err)
	}
	for arch, encoded := range first {
		got, err := os.ReadFile(filepath.Join(output, "linux-"+arch+".gz"))
		if err != nil || !bytes.Equal(got, encoded) {
			t.Fatal("helper packaging is not reproducible")
		}
	}
	badOutput := filepath.Join(dir, "must-not-exist")
	if err := helpers([]string{"-amd64", paths["amd64"], "-arm64", paths["amd64"], "-output", badOutput}); err == nil {
		t.Fatal("packaged wrong architecture")
	}
	if _, err := os.Stat(badOutput); !os.IsNotExist(err) {
		t.Fatal("packaging wrote files before validating both architectures")
	}
	// Compile real go:embed data, then verify inclusion in the linked artifact.
	// Changing an expected payload must fail even though both files still exist.
	embedSource := []byte("package main\nimport (\"embed\";\"fmt\")\n//go:embed payloads/*.gz\nvar assets embed.FS\nfunc main(){for _,arch:=range []string{\"amd64\",\"arm64\"}{data,_:=assets.ReadFile(\"payloads/linux-\"+arch+\".gz\");fmt.Println(len(data))}}\n")
	if err := os.WriteFile(source, embedSource, 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(dir, "launcher")
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-buildvcs=false", "-trimpath", "-o", launcher, source)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64", "GOWORK=off")
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build embedded fixture: %v\n%s", err, data)
	}
	if err := bundledHelpers([]string{"-directory", output, launcher}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(output, "linux-arm64.gz"), append(first["arm64"], 1), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := bundledHelpers([]string{"-directory", output, launcher}); err == nil {
		t.Fatal("accepted launcher with a different helper payload")
	}
}
