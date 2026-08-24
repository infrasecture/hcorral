package runtime

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/infrasecture/hcorral/internal/command"
)

type dockerRunner struct {
	results []command.Result
	errors  []error
	argv    [][]string
	env     [][]string
}

func (r *dockerRunner) Capture(_ context.Context, argv, env []string) (command.Result, error) {
	r.argv = append(r.argv, append([]string(nil), argv...))
	r.env = append(r.env, append([]string(nil), env...))
	result, err := r.results[0], r.errors[0]
	r.results, r.errors = r.results[1:], r.errors[1:]
	return result, err
}
func (*dockerRunner) Run(context.Context, []string, []string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("unexpected Run")
}
func (*dockerRunner) Replace([]string, []string) error { return errors.New("unexpected Replace") }

func TestDockerInspectionDecodesExactShapesAndStripsComposeEnvironment(t *testing.T) {
	t.Setenv("COMPOSE_FILE", "must-not-propagate")
	runner := &dockerRunner{
		results: []command.Result{
			{Stdout: []byte("abc\n")},
			{Stdout: []byte(`[{"Id":"abc","Name":"/hcorral-demo-aaaaaaa","Config":{"Image":"image:v1","Labels":{"a":"b"},"Env":["K=V"]},"State":{"Status":"running","Running":true,"StartedAt":"now"},"Mounts":[{"Type":"volume","Name":"state","Destination":"/home/a","RW":true}]}]`)},
		},
		errors: []error{nil, nil},
	}
	docker := NewDocker(runner)
	containers, err := docker.ListContainers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 || containers[0].CleanName() != "hcorral-demo-aaaaaaa" || !containers[0].State.Running || containers[0].Mounts[0].Name != "state" {
		t.Fatalf("decoded containers = %#v", containers)
	}
	if !reflect.DeepEqual(runner.argv[1], []string{"docker", "inspect", "--type", "container", "abc"}) {
		t.Fatalf("inspect argv = %#v", runner.argv[1])
	}
	for _, env := range runner.env {
		if strings.HasPrefix(strings.Join(env, "\n"), "COMPOSE_FILE=") || strings.Contains(strings.Join(env, "\n"), "\nCOMPOSE_FILE=") {
			t.Fatalf("Compose environment leaked: %#v", env)
		}
	}
}

func TestDockerNotFoundAndMalformedJSONAreDistinct(t *testing.T) {
	runner := &dockerRunner{
		results: []command.Result{{Stderr: []byte("Error: No such volume: absent")}, {Stdout: []byte(`{"not":"an-array"}`)}},
		errors:  []error{errors.New("exit 1"), nil},
	}
	docker := NewDocker(runner)
	volume, err := docker.InspectVolume(context.Background(), "absent")
	if err != nil || volume != nil {
		t.Fatalf("not found = %#v, %v", volume, err)
	}
	if _, err := docker.InspectContainers(context.Background(), "broken"); err == nil || !strings.Contains(err.Error(), "decode Docker container inspection") {
		t.Fatalf("malformed JSON error = %v", err)
	}
}

func TestInspectNetworkDecodesComposeOwnership(t *testing.T) {
	runner := &dockerRunner{
		results: []command.Result{{Stdout: []byte(`[{"Id":"network-id","Name":"demo_default","Driver":"bridge","Labels":{"com.docker.compose.project":"demo","com.docker.compose.network":"default"}}]`)}},
		errors:  []error{nil},
	}
	network, err := NewDocker(runner).InspectNetwork(context.Background(), "demo_default")
	if err != nil {
		t.Fatal(err)
	}
	if network.Name != "demo_default" || network.Driver != "bridge" || network.Labels["com.docker.compose.network"] != "default" {
		t.Fatalf("decoded network = %#v", network)
	}
}

func TestInspectNetworkTreatsMissingObjectAsAbsent(t *testing.T) {
	for _, diagnostic := range []string{
		`Error response from daemon: No such network: absent`,
		`Error response from daemon: network absent not found`,
	} {
		runner := &dockerRunner{
			results: []command.Result{{Stderr: []byte(diagnostic)}},
			errors:  []error{errors.New("exit 1")},
		}
		network, err := NewDocker(runner).InspectNetwork(context.Background(), "absent")
		if err != nil || network != nil {
			t.Fatalf("missing network for %q = %#v, %v", diagnostic, network, err)
		}
	}
}

var _ command.Runner = (*dockerRunner)(nil)
