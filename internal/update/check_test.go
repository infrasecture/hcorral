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
				if request.URL.String() != "https://registry.invalid/codex/latest" {
					t.Fatalf("request URL = %s", request.URL)
				}
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			checker := Checker{Docker: containerruntime.NewDocker(noImageRunner{}), Client: client, RegistryURL: "https://registry.invalid/codex/latest"}
			facts := checker.Inspect(context.Background(), config.Config{ImageName: "example.invalid/hcorral", ImageTag: "pinned", UpdateCheck: true}, nil)
			if !facts.Enabled || !facts.Pinned {
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
	if _, err := (Checker{Client: client}).latest(context.Background()); err == nil {
		t.Fatal("oversized invalid registry response unexpectedly decoded")
	}
}

var _ command.Runner = noImageRunner{}
var _ http.RoundTripper = roundTripFunc(nil)
