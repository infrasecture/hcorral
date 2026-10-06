#!/usr/bin/env bash
# Sent by the host launcher to bash inside the container. Keeping this script
# host-side lets existing workstation images display the same attach notice.
set -euo pipefail

session="$(tmux display-message -p -t "=$1:" '#{session_id}')"
mode="$2"
notice="$3"
details="${4:-}"
reopen="${5:-0}"

case "${mode}" in
  x11) badge='GUI:X11' ;;
  wayland) badge='GUI:WL' ;;
  none) badge='GUI:off' ;;
  *) badge='GUI:?' ;;
esac

# Keep Byobu/user status formatting and update one session only. The option
# reference makes repeated attaches idempotent without saving config files.
tmux set-option -t "${session}" @hcorral-gui "${badge}"
left="$(tmux show-options -Av -t "${session}" status-left)"
if [[ "${left}" != *'#{@hcorral-gui}'* ]]; then
  tmux set-option -t "${session}" status-left '#{@hcorral-gui} '"${left}"
fi
length="$(tmux show-options -Av -t "${session}" status-left-length)"
if (( length < 8 )); then
  tmux set-option -t "${session}" status-left-length 8
fi

# Retain the full text in the session, independent of pane scrollback and any
# alternate-screen application. Keep the last report when a later check is quiet.
if [[ -n "${details}" ]]; then
  tmux set-option -t "${session}" @hcorral-notices \
    "${notice}"$'\n\n'"${details}"$'\n\nPress q to close. Reopen with: hcorral notices'
elif [[ -z "$(tmux show-options -qv -t "${session}" @hcorral-notices)" ]]; then
  tmux set-option -t "${session}" @hcorral-notices "${notice}"
fi

# Queue UI in the attaching client, after tmux owns the screen. The popup reads
# a session option rather than interpolating notice text into a shell command.
# A pager keeps long reports scrollable and waits for explicit dismissal.
if [[ -n "${details}" || "${reopen}" == 1 ]]; then
  popup_supported=0
  command_supported=0
  while IFS= read -r capability; do
    case "${capability}" in
      display-popup\ *) popup_supported=1 ;;
      run-shell\ *) [[ "${capability}" != *C* ]] || command_supported=1 ;;
    esac
  done < <(tmux list-commands)
  if [[ "${popup_supported}${command_supported}" != 11 ]] || ! command -v less >/dev/null; then
    printf '%s\n' 'hcorral: retained reports require tmux display-popup, run-shell -C, and less; update the workstation image.' >&2
    exit 1
  fi
  # Let attach finish and send terminal queries before waiting for dismissal.
  # A popup in the initial command sequence delays those queries past tmux's
  # startup timeout, leaking device-attribute replies into the shell.
  exec tmux attach-session -t "${session}" \; \
    run-shell -b -C \
      "display-popup -E -w 90% -h 80% -T 'hcorral notices (q to close)' \
        \"tmux show-options -qv -t '${session}' @hcorral-notices | LESS= less -+F -+X\""
fi
exec tmux attach-session -t "${session}" \; display-message -d 8000 -l "${notice}"
