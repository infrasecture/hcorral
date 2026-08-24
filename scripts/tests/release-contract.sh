#!/usr/bin/env bash
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${root}"
fail(){ echo "FAIL: $*" >&2; exit 1; }

build_help="$(./build.sh --help)"
grep -Fq -- '--release' <<<"${build_help}" || fail 'build help lacks release mode'
grep -Fq -- '--packages' <<<"${build_help}" || fail 'build help lacks packages'
if grep -Eq 'docker (push|buildx imagetools create)' build.sh; then fail 'build.sh contains publication'; fi

release_help="$(./release.sh --help)"
grep -Fq -- '--prepare-only' <<<"${release_help}" || fail 'release help lacks prepare-only'
grep -Fq -- '--publish-prepared' <<<"${release_help}" || fail 'release help lacks publish-prepared'
grep -Fq 'AGPL-3.0-or-later' release.sh || fail 'formula license missing'
grep -Fq 'bin.install "hcorral"' release.sh || fail 'formula install contract missing'
grep -Fq 'publish-prepared' release.sh || fail 'publish phase missing'

while IFS= read -r use; do
  [[ "${use}" =~ @[0-9a-f]{40}$ ]] || fail "workflow action is not pinned to a full commit: ${use}"
done < <(sed -nE 's/^[[:space:]]*- uses: ([^#[:space:]]+).*$/\1/p' .github/workflows/*.yaml)

set +e
output="$(./release.sh --version v1.0 --prepare-only 2>&1)"; status=$?
set -e
[[ ${status} -ne 0 ]] || fail 'invalid version accepted'
grep -Fq 'canonical v-prefixed SemVer' <<<"${output}" || fail 'invalid version error unclear'

echo 'PASS: build and release command contracts'
