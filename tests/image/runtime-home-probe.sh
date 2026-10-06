#!/usr/bin/env bash
# Runs inside the integration image, as the user selected by the entrypoint.
set -euo pipefail
[[ "$(id -u)" == "$HCORRAL_HOST_UID" && "$(id -g)" == "$HCORRAL_HOST_GID" ]]
if [[ "${HCORRAL_TEST_LEGACY_IMAGE:-0}" == 1 ]]; then
  [[ ! -e /etc/hcorral/bashrc ]]
else
  [[ "$(stat -c '%u:%g:%a' /etc/hcorral/bashrc)" == 0:0:644 ]]
  if grep -Fq /home/vscode /usr/local/bin/entrypoint.sh; then exit 1; fi
fi
[[ "$(stat -c '%u:%g:%a' /etc/hcorral)" == 0:0:755 ]]
[[ "$(stat -c '%u:%g' "$HOME/.bashrc")" == "$HCORRAL_HOST_UID:$HCORRAL_HOST_GID" ]]

case "$HCORRAL_TEST_SCENARIO" in
  fresh|old-marker)
    grep -Fq '. /etc/hcorral/bashrc' "$HOME/.bashrc"
    [[ -s "$HOME/.profile" ]]
    ;;
  empty) [[ ! -s "$HOME/.bashrc" ]] ;;
  custom|symlink|bash-profile|bash-login)
    cmp "$HOME/custom-rc" "$HOME/.bashrc"
    [[ "$HCORRAL_TEST_CUSTOM" == yes && "$HCORRAL_TEST_PROFILE_LOADS" == 1 ]]
    if [[ "$HCORRAL_TEST_SCENARIO" == symlink ]]; then [[ -L "$HOME/.bashrc" ]]; fi
    if [[ "$HCORRAL_TEST_SCENARIO" == bash-* ]]; then [[ ! -e "$HOME/.profile" ]]; fi
    ;;
esac

# Inspect real first/new/split shells. Send assertions only into test-owned
# panes, and wait for marker files so a dead shell cannot look like a success.
panes=()
if [[ "${HCORRAL_TEST_SKIP_INITIAL_PANE:-0}" != 1 ]]; then
  panes+=("$(tmux display-message -p -t hcorral: '#{pane_id}')")
fi
second="$(tmux new-window -P -F '#{pane_id}' -t hcorral -c /test-workspace)"
third="$(tmux split-window -P -F '#{pane_id}' -t "$second" -c /test-workspace)"
panes+=("$second" "$third")
for pane in "${panes[@]}"; do
  marker="/tmp/shell-probe-${pane#%}"
  # shellcheck disable=SC2016 # These assertions execute in the pane shell.
  check='[[ $- == *i* ]] && shopt -q login_shell && [[ "$(id -u)" == "$HCORRAL_HOST_UID" ]]'
  case "$HCORRAL_TEST_SCENARIO" in
    fresh|old-marker)
      check+=' && shopt -q histappend'
      if [[ "$HCORRAL_HARNESS_TYPE" == codex ]]; then check+=' && complete -p codex >/dev/null'; fi
      ;;
    custom|symlink|bash-profile|bash-login)
      # shellcheck disable=SC2016
      check+=' && [[ "$HCORRAL_TEST_CUSTOM" == yes && "$HCORRAL_TEST_PROFILE_LOADS" == 1 ]]'
      ;;
  esac
  tmux send-keys -t "$pane" -l "$check && touch $marker"
  tmux send-keys -t "$pane" Enter
  for _ in {1..100}; do
    [[ -f "$marker" ]] && break
    sleep 0.05
  done
  if [[ ! -f "$marker" ]]; then
    tmux capture-pane -p -t "$pane" >&2
    exit 1
  fi
  if tmux capture-pane -p -t "$pane" | grep -Eq 'Permission denied|No such file or directory'; then exit 1; fi
done
printf 'PASS: %s:%s %s (legacy image: %s)\n' \
  "$HCORRAL_HOST_UID" "$HCORRAL_HOST_GID" "$HCORRAL_TEST_SCENARIO" "${HCORRAL_TEST_LEGACY_IMAGE:-0}"
