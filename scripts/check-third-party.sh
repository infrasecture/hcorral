#!/usr/bin/env bash
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${root}"

fail() {
  echo "ERROR: third-party inventory: $*" >&2
  exit 1
}

[[ -s THIRD_PARTY_LICENSES.md ]] || fail 'THIRD_PARTY_LICENSES.md is missing'
[[ "$(awk '$1 == "module" { count++ } END { print count + 0 }' go.mod)" == 1 ]] || fail 'unexpected Go module declaration'
if grep -Eq '^[[:space:]]*(require|replace|exclude|retract)[[:space:](]' go.mod; then
  fail 'Go module policy changed; inventory and compatibility review are required'
fi

extract_assignment() {
  local file="$1" name="$2" value
  value="$(sed -nE "s/^${name}=([^[:space:]]+)$/\\1/p" "${file}")"
  [[ -n "${value}" && "${value}" != *$'\n'* ]] || fail "cannot resolve one ${name} assignment from ${file}"
  printf '%s\n' "${value}"
}

extract_arg_default() {
  local name="$1" value
  value="$(sed -nE "s/^ARG ${name}=([^[:space:]]+)$/\\1/p" image/Dockerfile)"
  [[ -n "${value}" && "${value}" != *$'\n'* ]] || fail "cannot resolve one ${name} default from image/Dockerfile"
  printf '%s\n' "${value}"
}

node_version="$(extract_arg_default HCORRAL_NODE_VERSION)"
claude_version="$(extract_assignment scripts/build-workstation-image.sh CLAUDE_CODE_VERSION)"
gemini_version="$(extract_assignment scripts/build-workstation-image.sh GEMINI_CLI_VERSION)"
opencode_version="$(extract_assignment scripts/build-workstation-image.sh OPENCODE_VERSION)"

[[ "$(extract_arg_default HCORRAL_CLAUDE_CODE_VERSION)" == "${claude_version}" ]] || fail 'Claude Code version differs between Dockerfile and builder'
[[ "$(extract_arg_default HCORRAL_GEMINI_CLI_VERSION)" == "${gemini_version}" ]] || fail 'Gemini CLI version differs between Dockerfile and builder'
[[ "$(extract_arg_default HCORRAL_OPENCODE_VERSION)" == "${opencode_version}" ]] || fail 'OpenCode version differs between Dockerfile and builder'

for value in "${node_version}" "${claude_version}" "${gemini_version}" "${opencode_version}"; do
  grep -Fq -- "| ${value} |" THIRD_PARTY_LICENSES.md || fail "selected version ${value} is absent from THIRD_PARTY_LICENSES.md"
done

for required in \
  'OpenAI Codex CLI | selected per image release | Apache-2.0' \
  'Claude Code |' \
  'Anthropic commercial or consumer terms; proprietary' \
  'Gemini CLI |' \
  'Apache-2.0' \
  'OpenCode |' \
  '| MIT |' \
  '/usr/share/doc/<package>/copyright'; do
  grep -Fq -- "${required}" THIRD_PARTY_LICENSES.md || fail "required notice is absent: ${required}"
done

echo 'PASS: direct dependency versions and license classifications are inventoried'
