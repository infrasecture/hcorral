#!/usr/bin/env bash
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/lib/hcorral-image.sh
source "${root}/scripts/lib/hcorral-image.sh"

bash -n "${root}/scripts/build-harness-image.sh" "${root}/image/entrypoint.sh" "${root}/image/session-init.sh"
shellcheck "${root}/scripts/build-harness-image.sh" "${root}/image/entrypoint.sh" "${root}/image/session-init.sh"

for harness in codex claude pi; do
	grep -Fq "AS ${harness}" "${root}/image/Dockerfile"
	grep -Fq "ai.infrasecture.hcorral.harness.type=\"${harness}\"" "${root}/image/Dockerfile"
done
if grep -Eiq 'hermes|gemini|opencode|org.opencontainers.image.licenses' "${root}/image/Dockerfile"; then
	echo 'unexpected harness or image license label found' >&2
	exit 1
fi
if grep -Eq 'mkdir -p /workspace|WORKDIR /workspace|ln -s .* /workspace' "${root}/image/Dockerfile" "${root}/image/entrypoint.sh"; then
	echo 'unexpected /workspace compatibility path found' >&2
	exit 1
fi
grep -Fq 'DISABLE_AUTOUPDATER=1' "${root}/image/Dockerfile"
grep -Fq -- '--ignore-scripts' "${root}/image/Dockerfile"
grep -Fq '/etc/codex/config.toml' "${root}/image/entrypoint.sh"
# shellcheck disable=SC2016 # Match the literal expansion, not this test process.
if grep -Fq '${CODEX_HOME}/config.toml' "${root}/image/entrypoint.sh"; then
	echo 'entrypoint writes the persisted Codex user configuration' >&2
	exit 1
fi

[[ "$(hcorral_image_release_tag 1.2.3 4)" == 1.2.3-r4 ]]
[[ "$(hcorral_compare_image_releases 1.2.3 2 1.2.3 1)" == 1 ]]
if hcorral_validate_harness_version latest >/dev/null 2>&1; then
	echo 'non-exact version was accepted' >&2
	exit 1
fi

echo 'harness image contract tests passed'
