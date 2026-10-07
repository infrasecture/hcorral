#!/usr/bin/env bash
# Docker versions may enumerate mount maps in different orders on each inspect.
# Compare every mount field without interpreting array order as a state change.
container_snapshot() {
  docker inspect --format '{{.Id}}|{{.Image}}|{{.State.Status}}|{{.State.StartedAt}}|{{.State.FinishedAt}}{{println}}{{range .Mounts}}{{json .}}{{println}}{{end}}' "$1" | LC_ALL=C sort
}

assert_container_snapshot() {
  local after
  after="$(container_snapshot "$1")" || return
  if [[ "$after" != "$2" ]]; then
    printf 'Container state changed:\nBefore:\n%s\nAfter:\n%s\n' "$2" "$after" >&2
    return 1
  fi
}
