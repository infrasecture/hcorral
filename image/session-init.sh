#!/usr/bin/env bash
set -euo pipefail

status_file=/run/hcorral-startup-status

die() {
  printf 'hcorral: %s\n' "$*" >&2
  exit 2
}

required() {
  local name="$1"
  [[ -n "${!name:-}" ]] || die "missing ${name}; start this container through hcorral"
}

required HCORRAL_HOST_UID
required HCORRAL_CONTAINER_HOME
required HCORRAL_WORKDIR

runtime_uid="${HCORRAL_HOST_UID}"
runtime_home="${HCORRAL_CONTAINER_HOME}"
runtime_workdir="${HCORRAL_WORKDIR}"
session="${HCORRAL_BYOBU_SESSION:-hcorral}"
harness_type="${HCORRAL_HARNESS_TYPE:-}"

[[ "${runtime_uid}" =~ ^[0-9]+$ ]] || die "HCORRAL_HOST_UID must be numeric"
[[ "${session}" =~ ^[A-Za-z0-9_.-]{1,64}$ ]] || die "HCORRAL_BYOBU_SESSION must match [A-Za-z0-9_.-]{1,64}"
runtime_user="$(getent passwd "${runtime_uid}" | cut -d: -f1 || true)"
[[ -n "${runtime_user}" ]] || die "no runtime account exists for UID ${runtime_uid}"
[[ -d "${runtime_workdir}" ]] || die "workdir does not exist: ${runtime_workdir}"

as_runtime_user() {
	local -a runtime_env=(env HOME="${runtime_home}" USER="${runtime_user}" LOGNAME="${runtime_user}" SHELL=/bin/bash NPM_CONFIG_PREFIX="${runtime_home}/.local/share/npm" PATH="${runtime_home}/.local/bin:${runtime_home}/.local/share/npm/bin:${runtime_home}/.cargo/bin:${PATH}" HCORRAL_WORKDIR="${runtime_workdir}")
	if [[ "${harness_type}" == codex ]]; then runtime_env+=(CODEX_HOME="${runtime_home}/.codex"); fi
	if [[ "${harness_type}" == claude ]]; then runtime_env+=(DISABLE_AUTOUPDATER=1); fi
	gosu "${runtime_user}" "${runtime_env[@]}" "$@"
}

# shellcheck disable=SC2016 # Expanded inside the runtime user's shell.
runtime_shell_cmd='if [[ -f "${HOME}/.bashrc" ]]; then
  exec bash --login
fi
exec bash --rcfile /etc/bash.bashrc -i'

as_runtime_user byobu-ctrl-a screen >/dev/null 2>&1 || true

if ! as_runtime_user byobu-tmux has-session -t "${session}" 2>/dev/null; then
  # shellcheck disable=SC2016 # Expanded inside the runtime user's shell.
  startup_cmd='cd "${HCORRAL_WORKDIR}"
clear
cat /etc/hcorral/session-banner.txt'
  startup_cmd="${startup_cmd}"$'\n'"${runtime_shell_cmd}"
  as_runtime_user byobu-tmux new-session \
    -d \
    -s "${session}" \
    -c "${runtime_workdir}" \
    bash --login -lc "${startup_cmd}"
fi

as_runtime_user byobu-tmux set-option -t "${session}" default-shell /bin/bash >/dev/null
as_runtime_user byobu-tmux set-option -t "${session}" default-command "${runtime_shell_cmd}" >/dev/null
printf 'ready\n' >"${status_file}" 2>/dev/null || true
