#!/usr/bin/env bash

# shellcheck disable=SC2034 # Constants are consumed by scripts that source this file.
HCORRAL_DEFAULT_IMAGE_NAME="ghcr.io/infrasecture/hcorral"
HCORRAL_DEFAULT_CODEX_NPM_PACKAGE="@openai/codex"
HCORRAL_IMAGE_INPUTS_FILE=".hcorral-image-inputs"
HCORRAL_BUILD_INPUT_LABEL="ai.infrasecture.hcorral.build.input-digest"
HCORRAL_SOURCE_REVISION_LABEL="org.opencontainers.image.revision"

hcorral_validate_codex_version() {
  local version="$1"
  local without_build prerelease identifier
  local -a identifiers

  if [[ "${version}" =~ -r[0-9]+$ ]]; then
    echo "Codex version includes the reserved image revision suffix: ${version}" >&2
    echo "Pass it separately, for example: --version ${version%-r*} --revision ${version##*-r}" >&2
    return 1
  fi

  if [[ ! "${version}" =~ ^(0|[1-9][0-9]*)[.](0|[1-9][0-9]*)[.](0|[1-9][0-9]*)(-([0-9A-Za-z-]+([.][0-9A-Za-z-]+)*))?(\+([0-9A-Za-z-]+([.][0-9A-Za-z-]+)*))?$ ]]; then
	  echo "Codex version is not canonical SemVer: ${version}" >&2
	  return 1
	fi

  without_build="${version%%+*}"
  if [[ "${without_build}" == *-* ]]; then
    prerelease="${without_build#*-}"
    IFS='.' read -r -a identifiers <<<"${prerelease}"
    for identifier in "${identifiers[@]}"; do
      if [[ "${identifier}" =~ ^[0-9]+$ && "${#identifier}" -gt 1 && "${identifier}" == 0* ]]; then
	      echo "Codex version has a non-canonical numeric prerelease identifier: ${version}" >&2
	      return 1
	    fi
	  done
	fi
}

hcorral_validate_npm_package() {
	local package="$1"

	if [[ ! "${package}" =~ ^(@[a-z0-9._-]+/)?[a-z0-9._-]+$ ]]; then
		echo "Codex npm package name is invalid: ${package}" >&2
		return 1
	fi
}

hcorral_validate_boolean() {
	local name="$1"
	local value="$2"

	if [[ "${value}" != true && "${value}" != false ]]; then
		echo "${name} must be true or false (got ${value})" >&2
		return 1
	fi
}

hcorral_validate_image_revision() {
  local revision="$1"

  if [[ ! "${revision}" =~ ^[1-9][0-9]*$ ]]; then
    echo "Image revision must be a positive integer without leading zeros: ${revision}" >&2
    return 1
  fi
}

hcorral_validate_build_input_digest() {
  local digest="$1"

  if [[ ! "${digest}" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    echo "Image build-input digest is invalid: ${digest}" >&2
    return 1
  fi
}

hcorral_validate_source_revision() {
  local revision="$1"

  if [[ ! "${revision}" =~ ^[0-9a-f]{40}([0-9a-f]{24})?$ ]]; then
    echo "Image source revision is not a full Git object ID: ${revision}" >&2
    return 1
  fi
}

hcorral_sha256_stream() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{ print $1 }'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 | awk '{ print $1 }'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 | sed -nE 's/^.*= ([0-9a-f]+)$/\1/p'
  else
    echo "Cannot compute image build-input digest: sha256sum, shasum, or openssl is required" >&2
    return 1
  fi
}

hcorral_image_input_paths() {
  local project_root="$1"
  local manifest="${project_root}/${HCORRAL_IMAGE_INPUTS_FILE}"
  local path

  if [[ ! -f "${manifest}" ]]; then
    echo "Image input manifest not found: ${manifest}" >&2
    return 1
  fi

  printf '%s\n' "${HCORRAL_IMAGE_INPUTS_FILE}"
  while IFS= read -r path || [[ -n "${path}" ]]; do
    path="${path%$'\r'}"
    [[ -z "${path}" || "${path}" == \#* ]] && continue
    if [[ "${path}" == *$'\t'* || "${path}" == *$'\n'* ]]; then
      echo "Image input paths cannot contain tabs or newlines: ${path@Q}" >&2
      return 1
    fi
    if [[ "${path}" == /* || "${path}" == .. || "${path}" == ../* || "${path}" == */../* ]]; then
      echo "Invalid image input path in ${manifest}: ${path}" >&2
      return 1
    fi
    if [[ ! -e "${project_root}/${path}" && ! -L "${project_root}/${path}" ]]; then
      echo "Image input does not exist: ${path}" >&2
      return 1
    fi
    printf '%s\n' "${path}"
  done <"${manifest}"
}

hcorral_build_input_digest() {
	local project_root="$1"
	local path index_entry mode object_id digest paths_output records=""
	local codex_package install_claude install_gemini install_opencode
	local -a paths

  if ! command -v git >/dev/null 2>&1; then
    echo "Cannot identify image inputs without Git" >&2
    return 1
  fi
  if ! git -C "${project_root}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "Image build directory is not a Git worktree: ${project_root}" >&2
    return 1
  fi

  paths_output="$(hcorral_image_input_paths "${project_root}")" || return
  mapfile -t paths <<<"${paths_output}"
	for path in "${paths[@]}"; do
    index_entry="$(git -C "${project_root}" ls-files -s -- "${path}")"
    if [[ -z "${index_entry}" || "${index_entry}" == *$'\n'* ]]; then
      echo "Image input must be a single tracked path: ${path}" >&2
      return 1
    fi
    mode="${index_entry%% *}"
    object_id="$(git -C "${project_root}" hash-object --no-filters -- "${path}")" || return
		records+="${path}"$'\t'"${mode}"$'\t'"${object_id}"$'\n'
	done
	codex_package="${HCORRAL_CODEX_NPM_PACKAGE:-${HCORRAL_DEFAULT_CODEX_NPM_PACKAGE}}"
	install_claude="${HCORRAL_INSTALL_CLAUDE_CODE:-true}"
	install_gemini="${HCORRAL_INSTALL_GEMINI_CLI:-true}"
	install_opencode="${HCORRAL_INSTALL_OPENCODE:-true}"
	hcorral_validate_npm_package "${codex_package}" >/dev/null || return
	hcorral_validate_boolean HCORRAL_INSTALL_CLAUDE_CODE "${install_claude}" >/dev/null || return
	hcorral_validate_boolean HCORRAL_INSTALL_GEMINI_CLI "${install_gemini}" >/dev/null || return
	hcorral_validate_boolean HCORRAL_INSTALL_OPENCODE "${install_opencode}" >/dev/null || return
	records+="control"$'\t'"codex-package"$'\t'"${codex_package}"$'\n'
	records+="control"$'\t'"install-claude-code"$'\t'"${install_claude}"$'\n'
	records+="control"$'\t'"install-gemini-cli"$'\t'"${install_gemini}"$'\n'
	records+="control"$'\t'"install-opencode"$'\t'"${install_opencode}"$'\n'
	digest="$(printf 'hcorral-image-inputs-v1\n%s' "${records}" | hcorral_sha256_stream)" || return

  printf 'sha256:%s\n' "${digest}"
}

hcorral_source_revision() {
  local project_root="$1"
  local revision

  if ! revision="$(git -C "${project_root}" rev-parse --verify HEAD 2>/dev/null)"; then
    echo "Cannot resolve image source revision from ${project_root}" >&2
    return 1
  fi
  hcorral_validate_source_revision "${revision}" || return
  printf '%s\n' "${revision}"
}

hcorral_assert_clean_image_inputs() {
  local project_root="$1"
  local status paths_output
  local -a paths

  paths_output="$(hcorral_image_input_paths "${project_root}")" || return
  mapfile -t paths <<<"${paths_output}"
  status="$(git -C "${project_root}" status --porcelain=v1 --untracked-files=all -- "${paths[@]}")"
  if [[ -n "${status}" ]]; then
    echo "Published image inputs must be committed and clean:" >&2
    printf '%s\n' "${status}" >&2
    return 1
  fi
}

hcorral_image_release_tag() {
  local codex_version="$1"
  local image_revision="$2"

  hcorral_validate_codex_version "${codex_version}" || return
  hcorral_validate_image_revision "${image_revision}" || return
  printf '%s-r%s\n' "${codex_version}" "${image_revision}"
}

hcorral_compare_numeric_identifiers() {
  local left="$1"
  local right="$2"

  while [[ "${#left}" -gt 1 && "${left}" == 0* ]]; do
    left="${left#0}"
  done
  while [[ "${#right}" -gt 1 && "${right}" == 0* ]]; do
    right="${right#0}"
  done

  if [[ "${#left}" -lt "${#right}" ]]; then
    printf '%s\n' -1
  elif [[ "${#left}" -gt "${#right}" ]]; then
    printf '%s\n' 1
  elif [[ "${left}" == "${right}" ]]; then
    printf '%s\n' 0
  elif [[ "${left}" < "${right}" ]]; then
    printf '%s\n' -1
  else
    printf '%s\n' 1
  fi
}

# Prints -1, 0, or 1 using SemVer precedence. Versions are validated by the
# caller; build metadata is intentionally ignored for precedence.
hcorral_compare_semver() {
  local left="${1%%+*}"
  local right="${2%%+*}"
  local left_core="${left%%-*}"
  local right_core="${right%%-*}"
  local left_pre="" right_pre=""
  local comparison i left_part right_part
  local -a left_core_parts right_core_parts left_pre_parts right_pre_parts
  local LC_ALL=C

  if [[ "${left}" == *-* ]]; then
    left_pre="${left#*-}"
  fi
  if [[ "${right}" == *-* ]]; then
    right_pre="${right#*-}"
  fi

  IFS='.' read -r -a left_core_parts <<<"${left_core}"
  IFS='.' read -r -a right_core_parts <<<"${right_core}"
  for i in 0 1 2; do
    comparison="$(hcorral_compare_numeric_identifiers "${left_core_parts[$i]}" "${right_core_parts[$i]}")"
    if [[ "${comparison}" != 0 ]]; then
      printf '%s\n' "${comparison}"
      return
    fi
  done

  if [[ -z "${left_pre}" && -z "${right_pre}" ]]; then
    printf '%s\n' 0
    return
  elif [[ -z "${left_pre}" ]]; then
    printf '%s\n' 1
    return
  elif [[ -z "${right_pre}" ]]; then
    printf '%s\n' -1
    return
  fi

  IFS='.' read -r -a left_pre_parts <<<"${left_pre}"
  IFS='.' read -r -a right_pre_parts <<<"${right_pre}"
  for ((i = 0; i < ${#left_pre_parts[@]} || i < ${#right_pre_parts[@]}; i++)); do
    if [[ "${i}" -ge "${#left_pre_parts[@]}" ]]; then
      printf '%s\n' -1
      return
    elif [[ "${i}" -ge "${#right_pre_parts[@]}" ]]; then
      printf '%s\n' 1
      return
    fi

    left_part="${left_pre_parts[$i]}"
    right_part="${right_pre_parts[$i]}"
    if [[ "${left_part}" == "${right_part}" ]]; then
      continue
    fi

    if [[ "${left_part}" =~ ^[0-9]+$ && "${right_part}" =~ ^[0-9]+$ ]]; then
      hcorral_compare_numeric_identifiers "${left_part}" "${right_part}"
    elif [[ "${left_part}" =~ ^[0-9]+$ ]]; then
      printf '%s\n' -1
    elif [[ "${right_part}" =~ ^[0-9]+$ ]]; then
      printf '%s\n' 1
    elif [[ "${left_part}" < "${right_part}" ]]; then
      printf '%s\n' -1
    else
      printf '%s\n' 1
    fi
    return
  done

  printf '%s\n' 0
}

hcorral_compare_image_releases() {
  local left_version="$1"
  local left_revision="$2"
  local right_version="$3"
  local right_revision="$4"
  local comparison

  comparison="$(hcorral_compare_semver "${left_version}" "${right_version}")"
  if [[ "${comparison}" != 0 ]]; then
    printf '%s\n' "${comparison}"
    return
  fi

  hcorral_compare_numeric_identifiers "${left_revision}" "${right_revision}"
}

# Returns 0 when the registry reference exists, 1 when the registry explicitly
# reports it missing, and 2 when its state cannot be determined safely.
hcorral_registry_ref_exists() {
  local ref="$1"
  local output normalized

  if ! command -v docker >/dev/null 2>&1; then
    echo "ERROR: cannot inspect registry tag without the Docker CLI: ${ref}" >&2
    return 2
  fi

  if output="$(docker buildx imagetools inspect "${ref}" 2>&1)"; then
    return 0
  fi

  normalized="$(printf '%s' "${output}" | tr '[:upper:]' '[:lower:]')"
  case "${normalized}" in
    *": not found"*|*"manifest unknown"*|*"no such manifest"*)
      return 1
      ;;
  esac

  echo "ERROR: cannot determine whether registry tag exists: ${ref}" >&2
  if [[ -n "${output}" ]]; then
    printf '%s\n' "${output}" >&2
  fi
  return 2
}

hcorral_resolve_latest_codex_version() {
  local package="${HCORRAL_CODEX_NPM_PACKAGE:-${HCORRAL_DEFAULT_CODEX_NPM_PACKAGE}}"
  local package_path="${package}"
  local version

  if [[ "${package_path}" == @*/* ]]; then
    package_path="${package_path/\//%2F}"
  fi

  if command -v curl >/dev/null 2>&1; then
    version="$(
      curl --fail --silent --show-error --location --connect-timeout 5 --max-time 15 \
        "https://registry.npmjs.org/${package_path}/latest" \
        | sed -nE 's/.*"version"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' \
        | head -n 1 \
        | tr -d '[:space:]'
    )" || version=""
  fi

  if [[ -z "${version:-}" ]] && command -v npm >/dev/null 2>&1; then
    if command -v timeout >/dev/null 2>&1; then
      version="$(timeout 15 npm view "${package}" version --silent | tr -d '[:space:]')" || version=""
    elif command -v gtimeout >/dev/null 2>&1; then
      version="$(gtimeout 15 npm view "${package}" version --silent | tr -d '[:space:]')" || version=""
    else
      echo 'Cannot perform a bounded npm lookup: curl, timeout, or gtimeout is required' >&2
      return 1
    fi
  fi

  if [[ -z "${version:-}" ]]; then
    echo "Failed to resolve Codex version from npm registry: ${package}" >&2
    return 1
  fi

  hcorral_validate_codex_version "${version}" || return

  printf '%s\n' "${version}"
}
