package compose

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/infrasecture/hcorral/internal/command"
)

type failingCaptureRunner struct{}

func (failingCaptureRunner) Capture(context.Context, []string, []string) (command.Result, error) {
	return command.Result{Stderr: []byte("rendered-password=must-not-appear")}, errors.New("exit status 1")
}

func (failingCaptureRunner) Run(context.Context, []string, []string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("unexpected Run")
}

func (failingCaptureRunner) Replace([]string, []string) error {
	return errors.New("unexpected Replace")
}

func TestCaptureErrorDoesNotPromoteRenderedStderr(t *testing.T) {
	t.Parallel()
	project := Project{
		Runner:     failingCaptureRunner{},
		Invocation: Invocation{Prefix: []string{"docker", "compose"}, Project: "demo", Directory: "/work"},
		Out:        &bytes.Buffer{},
		Err:        &bytes.Buffer{},
	}
	result, err := project.Capture(context.Background(), "config", "--format", "json")
	if err == nil {
		t.Fatal("capture unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "must-not-appear") {
		t.Fatalf("captured stderr leaked through error: %v", err)
	}
	if !bytes.Contains(result.Stderr, []byte("must-not-appear")) {
		t.Fatal("caller lost captured diagnostics")
	}
}

var _ command.Runner = failingCaptureRunner{}

type renderedRunner struct{ document map[string]any }

func (r renderedRunner) Capture(_ context.Context, argv, _ []string) (command.Result, error) {
	if strings.Contains(strings.Join(argv, "\x00"), "config\x00--format\x00json") {
		content, err := json.Marshal(r.document)
		return command.Result{Stdout: content}, err
	}
	return command.Result{}, nil
}
func (renderedRunner) Run(context.Context, []string, []string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("unexpected Run")
}
func (renderedRunner) Replace([]string, []string) error { return errors.New("unexpected Replace") }

func TestRenderTreatsFinalTrustedOverlayAsTruth(t *testing.T) {
	t.Parallel()
	document := map[string]any{
		"services": map[string]any{
			"hcorral": map[string]any{"image": "user.example/custom:v2", "privileged": true},
			"sidecar": map[string]any{"image": "user.example/sidecar:v1"},
		},
		"volumes":  map[string]any{"custom": map[string]any{"name": "user-volume", "external": true}},
		"networks": map[string]any{"custom": map[string]any{"name": "user-network", "external": true}},
	}
	project := Project{Runner: renderedRunner{document: document}, Invocation: Invocation{Prefix: []string{"docker", "compose"}}}
	rendered, err := project.Render(context.Background())
	if err != nil {
		t.Fatalf("trusted overlay was rejected: %v", err)
	}
	if rendered.Services["hcorral"].Image != "user.example/custom:v2" || rendered.Services["sidecar"].Image != "user.example/sidecar:v1" {
		t.Fatalf("rendered services = %#v", rendered.Services)
	}
	if !rendered.Volumes["custom"].External || !rendered.Networks["custom"].External {
		t.Fatalf("rendered resources = %#v %#v", rendered.Volumes, rendered.Networks)
	}
}

var _ command.Runner = renderedRunner{}

func TestRenderAllowsSlowComposeWrapper(t *testing.T) {
	t.Parallel()
	// Configuration rendering is required setup, not an optional version probe.
	// A cold Compose plugin or policy wrapper can legitimately exceed five
	// seconds before returning its final configuration.
	const wrapper = `
case "$*" in
  *--hash*) printf 'hcorral fixture-hash\n' ;;
  *) sleep 6; printf '%s\n' '{"services":{"hcorral":{"image":"fixture:slow"}}}' ;;
esac`
	project := Project{Runner: command.ExecRunner{}, Invocation: Invocation{Prefix: []string{"sh", "-c", wrapper, "compose-fixture"}}}
	rendered, err := project.Render(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rendered.Services["hcorral"].Image != "fixture:slow" || rendered.Hashes["hcorral"] != "fixture-hash" {
		t.Fatalf("slow renderer lost configuration: %#v", rendered)
	}
}

func TestComposeCaptureHonorsShorterCallerDeadline(t *testing.T) {
	t.Parallel()
	project := Project{Runner: command.ExecRunner{}, Invocation: Invocation{Prefix: []string{"sh", "-c", "exec sleep 60", "compose-fixture"}}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := project.Capture(ctx, "config", "--format", "json")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stalled Compose did not report the caller's deadline: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("stalled Compose outlived the caller's deadline: %s", elapsed)
	}
}
