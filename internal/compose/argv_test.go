package compose

import (
	"reflect"
	"testing"
)

func TestInvocationArgsPreservesElements(t *testing.T) {
	t.Parallel()
	i := Invocation{Prefix: []string{"/policy compose", "compose"}, Project: "hcorral_demo-1234567", Directory: "/tmp/space here", Files: []string{"/cache/base.yaml", "/tmp/a b.yaml"}}
	want := []string{"/policy compose", "compose", "-p", "hcorral_demo-1234567", "--project-directory", "/tmp/space here", "-f", "/cache/base.yaml", "-f", "/tmp/a b.yaml", "up", "-d"}
	if got := i.Args("up", "-d"); !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %#v, want %#v", got, want)
	}
}

func TestMergeEnvironmentRemovesComposeAndOverridesManaged(t *testing.T) {
	t.Parallel()
	got := mergeEnvironment([]string{"PATH=/bin", "COMPOSE_FILE=bad", "HCORRAL_IMAGE_TAG=bad"}, map[string]string{"HCORRAL_IMAGE_TAG": "latest"})
	want := []string{"PATH=/bin", "HCORRAL_IMAGE_TAG=latest"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env = %#v, want %#v", got, want)
	}
}
