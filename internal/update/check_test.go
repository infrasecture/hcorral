package update

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/config"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

type noImageRunner struct{}

func (noImageRunner) Capture(context.Context, []string, []string) (command.Result, error) {
	return command.Result{Stderr: []byte("No such image")}, errors.New("exit status 1")
}

func (noImageRunner) Run(context.Context, []string, []string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("unexpected Run")
}

func (noImageRunner) Replace([]string, []string) error { return errors.New("unexpected Replace") }

type installedVersionRunner struct{}

func (installedVersionRunner) Capture(_ context.Context, argv, _ []string) (command.Result, error) {
	joined := strings.Join(argv, " ")
	if strings.HasPrefix(joined, "docker image inspect ") {
		return command.Result{Stdout: []byte(`[{"Id":"sha256:image","RepoDigests":[],"Config":{"Labels":{"ai.infrasecture.hcorral.harness.version":"1.2.3"}}}]`)}, nil
	}
	if strings.HasPrefix(joined, "docker exec ") {
		return command.Result{Stdout: []byte("codex-cli 1.3.0\n")}, nil
	}
	return command.Result{}, errors.New("unexpected capture: " + joined)
}

func (installedVersionRunner) Run(context.Context, []string, []string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("unexpected Run")
}

func (installedVersionRunner) Replace([]string, []string) error {
	return errors.New("unexpected Replace")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestInspectReportsBoundedLookupFacts(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"valid":   `{"version":"1.2.3"}`,
		"invalid": `{"version":"not-semver"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				switch request.URL.String() {
				case "https://api.github.com/repos/infrasecture/hcorral/releases/latest":
					return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(`{"tag_name":"v0.2.0"}`)), Header: make(http.Header)}, nil
				case "https://registry.invalid/codex/latest":
					return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
				default:
					t.Fatalf("request URL = %s", request.URL)
					return nil, errors.New("unexpected request")
				}
			})}
			checker := Checker{Docker: containerruntime.NewDocker(noImageRunner{}), Client: client, RegistryURL: "https://registry.invalid/codex/latest", LauncherVersion: "v0.1.0"}
			facts := checker.Inspect(context.Background(), config.Config{Harness: "codex", Image: "example.invalid/hcorral:pinned", UpdateCheck: true}, nil)
			if !facts.Enabled || !facts.Pinned || facts.LauncherLatest != "v0.2.0" || !facts.LauncherNewer {
				t.Fatalf("basic facts = %#v", facts)
			}
			if name == "valid" && (facts.LookupStatus != "ok" || facts.Latest != "1.2.3") {
				t.Fatalf("valid lookup facts = %#v", facts)
			}
			if name == "invalid" && (facts.LookupStatus != "unavailable" || facts.LookupErrorKind != "invalid-response" || facts.Latest != "") {
				t.Fatalf("invalid lookup facts = %#v", facts)
			}
		})
	}
}

func TestLatestLimitsResponseBody(t *testing.T) {
	t.Parallel()
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), (1<<20)+1))), Header: make(http.Header)}, nil
	})}
	if _, err := (Checker{Client: client}).latest(context.Background(), "codex"); err == nil {
		t.Fatal("oversized invalid registry response unexpectedly decoded")
	}
}

func TestInspectPrefersRunningInstalledVersionOverImageLabel(t *testing.T) {
	t.Parallel()
	container := &containerruntime.Container{Name: "/hcorral-demo-aaaaaaa"}
	container.Config.Image = "example.invalid/hcorral-codex:1.2.3"
	container.Config.Env = []string{"HCORRAL_HOST_UID=1000"}
	container.State.Running = true
	checker := Checker{Docker: containerruntime.NewDocker(installedVersionRunner{})}
	facts := checker.Inspect(context.Background(), config.Config{Harness: "codex", Image: container.Config.Image, UpdateCheck: false}, container)
	if facts.Current != "1.3.0" || facts.Selected != "1.2.3" {
		t.Fatalf("version facts = %#v", facts)
	}
}

var _ command.Runner = noImageRunner{}
var _ command.Runner = installedVersionRunner{}
var _ http.RoundTripper = roundTripFunc(nil)
