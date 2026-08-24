#!/usr/bin/env bash
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${root}"
fail() { echo "ERROR: third-party inventory: $*" >&2; exit 1; }

[[ -s THIRD_PARTY_LICENSES.md ]] || fail 'THIRD_PARTY_LICENSES.md is missing'
grep -Fq 'github.com/pelletier/go-toml/v2' go.mod || fail 'TOML dependency missing from go.mod'
grep -Fq 'github.com/pelletier/go-toml/v2' THIRD_PARTY_LICENSES.md || fail 'TOML dependency missing from inventory'
node_version="$(sed -nE 's/^ARG HCORRAL_NODE_VERSION=([^[:space:]]+)$/\1/p' image/Dockerfile)"
[[ -n "${node_version}" ]] || fail 'cannot resolve Node version'
grep -Fq "| Node.js | ${node_version} |" THIRD_PARTY_LICENSES.md || fail 'Node version differs from inventory'

for required in \
  'OpenAI Codex CLI | selected per image release | Apache-2.0' \
  'Claude Code | selected per image release | Anthropic commercial or consumer terms; proprietary' \
  'Pi coding agent | selected per image release | MIT' \
  '/usr/share/doc/<package>/copyright'; do
  grep -Fq -- "${required}" THIRD_PARTY_LICENSES.md || fail "required notice is absent: ${required}"
done
! grep -Eiq 'Gemini CLI|OpenCode|Hermes' THIRD_PARTY_LICENSES.md || fail 'removed harness remains inventoried'
echo 'PASS: direct dependency versions and license classifications are inventoried'
