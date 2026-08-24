#!/bin/sh
set -eu
session="${HCORRAL_BYOBU_SESSION:-hcorral}"
if ! tmux has-session -t "${session}" 2>/dev/null; then
  tmux new-session -d -s "${session}" sh
fi
printf 'ready\n' >/run/hcorral-startup-status
