package command

import (
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
