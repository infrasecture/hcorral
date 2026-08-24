#!/usr/bin/env bash

hcorral_is_stable_version() {
  [[ "${1:-}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]
}

hcorral_require_stable_version() {
  hcorral_is_stable_version "${1:-}" || {
    printf 'ERROR: release version must be canonical v-prefixed SemVer (got %s)\n' "${1:-<empty>}" >&2
    return 1
  }
}

hcorral_sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'; return; fi
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'; return; fi
  printf 'ERROR: sha256sum or shasum is required\n' >&2
  return 1
}

hcorral_state_value() {
  local file="$1" key="$2"
  [[ "${key}" =~ ^[A-Z][A-Z0-9_]*$ ]] || return 2
  awk -F= -v key="${key}" '$1 == key { count++; print substr($0,length(key)+2) } END { if(count != 1) exit 1 }' "${file}"
}
