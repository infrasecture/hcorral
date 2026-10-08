#!/usr/bin/env bash
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${root}"
builder="${HCORRAL_GOLANG_IMAGE:-golang:1.25.13-alpine@sha256:1e0126852075c9c60731c8ba49088448b91f63e2aed97ca9d1a9791622a05946}"

cache="${HCORRAL_GO_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/hcorral/go}"
mkdir -p "$cache/mod" "$cache/build"
cache="$(cd "$cache" && pwd)"
go_args=(--rm --volume "${root}:/src:ro" --workdir /src --env GOWORK=off --env HOME=/tmp --env GOPATH=/tmp/gopath
  --env GOMODCACHE=/go/pkg/mod --env GOCACHE=/tmp/go-build
  --volume "$cache/mod:/go/pkg/mod" --volume "$cache/build:/tmp/go-build")
run_go() { docker run "${go_args[@]}" --user "$(id -u):$(id -g)" "${builder}" "$@"; }

formatting="$(run_go sh -c 'gofmt -l cmd internal tests/integration')"
[[ -z "${formatting}" ]] || { echo "ERROR: gofmt required: ${formatting}" >&2; exit 1; }
# shellcheck disable=SC2016 # Executed inside the pinned builder.
run_go sh -c 'work=$(mktemp -d); cp -a /src/go.mod /src/go.sum /src/cmd /src/internal /src/tests "$work/"; cd "$work"; go mod tidy; diff -u /src/go.mod go.mod; if [[ -f /src/go.sum || -f go.sum ]]; then diff -u /src/go.sum go.sum; fi'
run_go go vet ./...
run_go go test -count=1 -timeout=90s ./...
docker run "${go_args[@]}" "${builder}" sh -c 'apk add --no-cache gcc musl-dev su-exec >/dev/null && exec su-exec "$1:$2" env CGO_ENABLED=1 go test -race -count=1 -timeout=90s ./...' sh "$(id -u)" "$(id -g)"
# A fixed iteration budget exercises the fuzz engine without coupling success
# to hosted-runner scheduling delays at a short wall-clock deadline.
run_go go test ./internal/update -run '^$' -fuzz '^FuzzParse$' -fuzztime=25000x

shellcheck -x build.sh release.sh image/*.sh internal/app/assets/*.sh scripts/*.sh scripts/lib/*.sh scripts/tests/*.sh tests/*.sh tests/image/*.sh tests/integration/*.sh tests/qualification/*.sh tests/fixtures/minimal-image/*.sh
for script in build.sh release.sh image/*.sh internal/app/assets/*.sh scripts/*.sh scripts/lib/*.sh scripts/tests/*.sh tests/*.sh tests/image/*.sh tests/integration/*.sh tests/qualification/*.sh tests/fixtures/minimal-image/*.sh; do
  bash -n "${script}"
done
python3 tests/tmux-notices_test.py
python3 tests/integration/attach-probe_test.py
scripts/check-third-party.sh
scripts/check-provenance.sh
scripts/tests/release-contract.sh
scripts/tests/image-versioning.sh
run_go go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 -config-file .github/actionlint.yaml .github/workflows/*.yaml

grep -Fq 'GNU AFFERO GENERAL PUBLIC LICENSE' LICENSE
grep -Fq 'AGPL-3.0-or-later' README.md
[[ -s THIRD_PARTY_LICENSES.md && -s docs/provenance.md ]]
# The transition fixture deliberately invokes the pinned original launcher.
if git grep -nE 'io\.infrasecture\.hcorral|com\.infrasecture\.hcorral|MYCODEX_' -- ':!docs/provenance.md' ':!internal/legacyguard/**' ':!tests/qualification/mycodex-transition.sh' ':!scripts/ci-source.sh'; then
  echo 'ERROR: stale or noncanonical hcorral namespace found' >&2
  exit 1
fi

if [[ -n "${HCORRAL_SKIP_VULNCHECK:-}" ]]; then
  echo 'WARNING: reachable vulnerability gate skipped; this is forbidden for public releases.' >&2
else
  set +e
  # Execute the tool directly: go run would replace its finding exit code with 1.
  # shellcheck disable=SC2016 # GOBIN is temporary inside the builder.
  vuln_output="$(run_go sh -c 'GOBIN=$(mktemp -d); export GOBIN; go install golang.org/x/vuln/cmd/govulncheck@v1.6.0 && exec "$GOBIN/govulncheck" ./...' 2>&1)"
  vuln_status=$?
  set -e
  if [[ ${vuln_status} -ne 0 && ${vuln_status} -ne 3 ]]; then printf '%s\n' "${vuln_output}" >&2; exit "${vuln_status}"; fi
  if [[ ${vuln_status} -eq 3 ]]; then
    reachable="$(printf '%s\n' "${vuln_output}" | grep '^Vulnerability #' | grep -oE 'GO-[0-9]{4}-[0-9]+' | sort -u || true)"
    allowed="$(sed -E 's/#.*//; s/[[:space:]]+//g' .govulncheck-allowlist | grep -E '^GO-[0-9]{4}-[0-9]+$' | sort -u || true)"
    blocking="$(comm -23 <(printf '%s\n' "${reachable}") <(printf '%s\n' "${allowed}") || true)"
    [[ -z "${blocking}" ]] || { printf 'ERROR: reachable vulnerabilities are not allowlisted:\n%s\n' "${blocking}" >&2; exit 1; }
  fi
fi

echo 'PASS: source, test, race, fuzz, shell, license, namespace, and vulnerability gates'
