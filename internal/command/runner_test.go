package command

import (
	"bytes"
	"context"
	"reflect"
	"testing"
)

func TestEnvironmentWithoutCompose(t *testing.T) {
	t.Parallel()
	input := []string{"PATH=/bin", "COMPOSE_FILE=bad", "DOCKER_HOST=unix:///x", "COMPOSE_PROFILES=x"}
	want := []string{"PATH=/bin", "DOCKER_HOST=unix:///x"}
	if got := EnvironmentWithoutCompose(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered environment = %#v, want %#v", got, want)
	}
}

func TestExecRunnerPreservesArgumentsEnvironmentAndStdio(t *testing.T) {
	t.Parallel()
	argv := []string{"sh", "-c", `printf '%s\0' "$@"; printf '%s' "$HCORRAL_TEST_VALUE" >&2; cat`, "sh", "space arg", "line\nbreak", ""}
	env := []string{"PATH=/usr/bin:/bin", "HCORRAL_TEST_VALUE=environment value"}
	input := bytes.NewBufferString("stdin value")
	var stdout, stderr bytes.Buffer
	if err := (ExecRunner{}).Run(context.Background(), argv, env, input, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	wantPrefix := []byte("space arg\x00line\nbreak\x00\x00")
	if !bytes.HasPrefix(stdout.Bytes(), wantPrefix) || !bytes.HasSuffix(stdout.Bytes(), []byte("stdin value")) {
		t.Fatalf("stdout did not preserve argv/stdin: %q", stdout.Bytes())
	}
	if stderr.String() != "environment value" {
		t.Fatalf("stderr/environment = %q", stderr.String())
	}
}
