#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
project_root="$(cd -- "${script_dir}/.." && pwd)"
# Path is resolved from this script at runtime.
# shellcheck disable=SC1091
source "${script_dir}/lib/hcorral-image.sh"

harness=""
version=""
revision="${HCORRAL_IMAGE_REVISION:-auto}"
push=false
manifest_only=false
resolve_only=false
advance_aliases=true
archs="${HCORRAL_IMAGE_ARCHS:-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')}"
release_archs="${HCORRAL_IMAGE_RELEASE_ARCHS:-amd64 arm64}"

usage() {
	cat <<'EOF'
Usage: build-harness-image.sh --harness codex|claude|pi [options]

  --version <semver>       Exact upstream harness version (default: current upstream)
  --revision <N|auto>      Image recipe revision (default: auto)
  --arch <amd64|arm64>     Architecture to build; repeatable
  --release                Build the configured release architecture set
  --push                   Push immutable architecture tags, then finalize if complete
  --manifest               Finalize an already-pushed architecture set and aliases
  --resolve-only           Print the resolved release identity
  --no-aliases             Do not advance <version> or latest during finalization
  --refresh-tags           Rebuild a matching local tag
EOF
}

explicit_archs=()
while (($#)); do
	case "$1" in
		--harness) harness="${2:-}"; shift 2 ;;
		--harness=*) harness="${1#*=}"; shift ;;
		--version) version="${2:-}"; shift 2 ;;
		--version=*) version="${1#*=}"; shift ;;
		--revision) revision="${2:-}"; shift 2 ;;
		--revision=*) revision="${1#*=}"; shift ;;
		--arch) explicit_archs+=("${2:-}"); shift 2 ;;
		--release) archs="${release_archs}"; shift ;;
		--push) push=true; shift ;;
		--manifest) manifest_only=true; shift ;;
		--resolve-only) resolve_only=true; shift ;;
		--no-aliases) advance_aliases=false; shift ;;
		--refresh-tags|--force) shift ;;
		-h|--help) usage; exit 0 ;;
		*) printf 'Unknown argument: %s\n' "$1" >&2; usage >&2; exit 2 ;;
	esac
done
if ((${#explicit_archs[@]})); then archs="${explicit_archs[*]}"; fi
case "${harness}" in
	codex|claude|pi) ;;
	*) echo "--harness must be codex, claude, or pi" >&2; exit 2 ;;
esac
for arch in ${archs} ${release_archs}; do
	case "${arch}" in amd64|arm64) ;; *) echo "unsupported image architecture: ${arch}" >&2; exit 2 ;; esac
done
if [[ "${manifest_only}" == true && "${push}" == true ]]; then echo "--manifest and --push are mutually exclusive" >&2; exit 2; fi

image_name="${HCORRAL_IMAGE_REPOSITORY:-ghcr.io/infrasecture/hcorral-${harness}}"
version_arg=""
case "${harness}" in
	codex) version_arg=HCORRAL_CODEX_VERSION ;;
	claude) version_arg=HCORRAL_CLAUDE_VERSION ;;
	pi) version_arg=HCORRAL_PI_VERSION ;;
esac

resolve_latest() {
	case "${harness}" in
		codex) curl -fsSL https://api.github.com/repos/openai/codex/releases/latest | python3 -c 'import json,sys; print(json.load(sys.stdin)["tag_name"].removeprefix("rust-v"))' ;;
		claude) curl -fsSL https://downloads.claude.ai/claude-code-releases/latest ;;
		pi) curl -fsSL https://registry.npmjs.org/@earendil-works%2Fpi-coding-agent/latest | python3 -c 'import json,sys; print(json.load(sys.stdin)["version"])' ;;
	esac
}
if [[ -z "${version}" ]]; then version="$(resolve_latest)"; fi
hcorral_validate_harness_version "${version}" || exit 2
if [[ "${revision}" != auto ]]; then hcorral_validate_image_revision "${revision}" || exit 2; fi

build_input_digest="$(hcorral_build_input_digest "${project_root}")"
source_revision="$(hcorral_source_revision "${project_root}")"
hcorral_validate_build_input_digest "${build_input_digest}" || exit 2

remote_version="" remote_revision="" remote_digest=""
remote_identity() {
	local ref="$1" output version_value revision_value digest_value harness_value
	if ! output="$(docker buildx imagetools inspect "${ref}" --format '{{printf "%s|%s|%s|%s\n" (index .Image.Config.Labels "ai.infrasecture.hcorral.harness.version") (index .Image.Config.Labels "ai.infrasecture.hcorral.image.revision") (index .Image.Config.Labels "ai.infrasecture.hcorral.build.input-digest") (index .Image.Config.Labels "ai.infrasecture.hcorral.harness.type")}}' 2>/dev/null)"; then
		if ! output="$(docker buildx imagetools inspect "${ref}" --format '{{range $platform, $image := .Image}}{{printf "%s|%s|%s|%s\n" (index $image.Config.Labels "ai.infrasecture.hcorral.harness.version") (index $image.Config.Labels "ai.infrasecture.hcorral.image.revision") (index $image.Config.Labels "ai.infrasecture.hcorral.build.input-digest") (index $image.Config.Labels "ai.infrasecture.hcorral.harness.type")}}{{end}}' 2>/dev/null)"; then return 1; fi
	fi
	remote_version=""; remote_revision=""; remote_digest=""
	while IFS='|' read -r version_value revision_value digest_value harness_value; do
		[[ "${harness_value}" == "${harness}" ]] || return 2
		if [[ -n "${remote_version}" ]] && { [[ "${remote_version}" != "${version_value}" ]] || [[ "${remote_revision}" != "${revision_value}" ]] || [[ "${remote_digest}" != "${digest_value}" ]]; }; then return 2; fi
		remote_version="${version_value}"; remote_revision="${revision_value}"; remote_digest="${digest_value}"
	done <<<"${output}"
	[[ -n "${remote_version}" ]] || return 2
}

remote_exists() { docker buildx imagetools inspect "$1" >/dev/null 2>&1; }
matches_current() {
	remote_identity "$1" || return
	[[ "${remote_version}" == "${version}" && "${remote_revision}" == "$2" && "${remote_digest}" == "${build_input_digest}" ]]
}

if [[ "${revision}" == auto ]]; then
	revision=1
	alias_ref="${image_name}:${version}"
	if remote_exists "${alias_ref}"; then
		remote_identity "${alias_ref}" || { echo "cannot read hcorral identity from ${alias_ref}" >&2; exit 1; }
		if [[ "${remote_version}" == "${version}" && "${remote_digest}" == "${build_input_digest}" ]]; then revision="${remote_revision}"; else revision="$((10#${remote_revision} + 1))"; fi
	fi
	for _ in $(seq 1 100); do
		candidate="${image_name}:${version}-r${revision}"
		conflict=false
		for candidate_ref in "${candidate}" "${candidate}-amd64" "${candidate}-arm64"; do
			if remote_exists "${candidate_ref}" && ! matches_current "${candidate_ref}" "${revision}"; then
				conflict=true
				break
			fi
		done
		if [[ "${conflict}" == false ]]; then break; fi
		revision="$((10#${revision} + 1))"
	done
fi
hcorral_validate_image_revision "${revision}" || exit 2
release_tag="${version}-r${revision}"

printf 'Harness image identity:\n  harness: %s\n  version: %s\n  revision: %s\n  input digest: %s\n  source: %s\n' "${harness}" "${version}" "${revision}" "${build_input_digest}" "${source_revision}"
if [[ "${resolve_only}" == true ]]; then
	printf 'harness=%s\nversion=%s\nrevision=%s\nbuild_input_digest=%s\nsource_revision=%s\nimage=%s\n' "${harness}" "${version}" "${revision}" "${build_input_digest}" "${source_revision}" "${image_name}"
	exit 0
fi

codex_checksum_args=()
if [[ "${harness}" == codex ]]; then
	release_json="$(curl -fsSL "https://api.github.com/repos/openai/codex/releases/tags/rust-v${version}")"
	for pair in 'amd64 x86_64' 'arm64 aarch64'; do
		read -r docker_arch upstream_arch <<<"${pair}"
		digest="$(python3 -c 'import json,sys; d=json.load(sys.stdin); name=sys.argv[1]; print(next(a["digest"].removeprefix("sha256:") for a in d["assets"] if a["name"] == name))' "codex-${upstream_arch}-unknown-linux-musl.tar.gz" <<<"${release_json}")"
		codex_checksum_args+=(--build-arg "HCORRAL_CODEX_SHA256_${docker_arch^^}=${digest}")
	done
fi

build_arch() {
	local arch="$1"
	local ref="${image_name}:${release_tag}-${arch}"
	local output=(--load)
	if [[ "${push}" == true ]] && remote_exists "${ref}"; then
		matches_current "${ref}" "${revision}" || { echo "immutable tag conflict: ${ref}" >&2; return 1; }
		echo "Keeping matching immutable tag ${ref}"; return
	fi
	if [[ "${push}" == true ]]; then output=(--push); fi
	docker buildx build --platform "linux/${arch}" "${output[@]}" --target "${harness}" \
		--file "${project_root}/image/Dockerfile" \
		--build-arg "${version_arg}=${version}" --build-arg "HCORRAL_IMAGE_REVISION=${revision}" \
		--build-arg "HCORRAL_SOURCE_REVISION=${source_revision}" --build-arg "HCORRAL_BUILD_INPUT_DIGEST=${build_input_digest}" \
		"${codex_checksum_args[@]}" --tag "${ref}" "${project_root}"
	if [[ "${push}" != true ]]; then
		docker run --rm --entrypoint bash "${ref}" -c "test ! -e /workspace; command -v ${harness}; ${harness} --version; for tool in codex claude pi; do [[ \"\$tool\" == ${harness} ]] || ! command -v \"\$tool\"; done"
		docker run --rm --entrypoint "${harness}" "${ref}" --version | grep -F "${version}"
		docker image tag "${ref}" "${image_name}:${release_tag}"
	fi
}

finalize() {
	local sources=() ref immutable="${image_name}:${release_tag}"
	for arch in ${release_archs}; do
		ref="${image_name}:${release_tag}-${arch}"
		remote_exists "${ref}" || { echo "cannot finalize; missing ${ref}" >&2; return 1; }
		matches_current "${ref}" "${revision}" || { echo "architecture identity mismatch: ${ref}" >&2; return 1; }
		sources+=("${ref}")
	done
	if remote_exists "${immutable}"; then matches_current "${immutable}" "${revision}" || { echo "immutable manifest conflict: ${immutable}" >&2; return 1; }; else docker buildx imagetools create --tag "${immutable}" "${sources[@]}"; fi
	if [[ "${advance_aliases}" != true ]]; then
		echo "Immutable manifest finalized without moving aliases: ${immutable}"
		return
	fi
	for alias in "${image_name}:${version}" "${image_name}:latest"; do
		if remote_exists "${alias}"; then
			remote_identity "${alias}" || { echo "refuse alias with unknown identity: ${alias}" >&2; return 1; }
			comparison="$(hcorral_compare_image_releases "${version}" "${revision}" "${remote_version}" "${remote_revision}")"
			if ((comparison < 0)); then echo "Keeping newer alias ${alias}"; continue; fi
		fi
		docker buildx imagetools create --tag "${alias}" "${immutable}"
	done
}

if [[ "${manifest_only}" == true ]]; then finalize; exit; fi
for arch in ${archs}; do build_arch "${arch}"; done
if [[ "${push}" == true ]]; then
	complete=true; for arch in ${release_archs}; do remote_exists "${image_name}:${release_tag}-${arch}" || complete=false; done
	if [[ "${complete}" == true ]]; then finalize; else echo "Release is pending remaining architecture tags."; fi
fi
