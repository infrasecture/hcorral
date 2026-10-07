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

# Render from the exact two archives produced by this build. Keep formula
# generation independent of publication so ordinary CI qualifies the same bytes.
hcorral_write_homebrew_formula() {
  local version="$1" directory="$2" pkg_version="${1#v}" amd_sha arm_sha
  hcorral_require_stable_version "${version}" || return
  amd_sha="$(hcorral_sha256_file "${directory}/hcorral_${pkg_version}_darwin_amd64.tar.gz")" || return
  arm_sha="$(hcorral_sha256_file "${directory}/hcorral_${pkg_version}_darwin_arm64.tar.gz")" || return
  mkdir -p "${directory}/Formula"
  cat >"${directory}/Formula/hcorral.rb" <<EOF
class Hcorral < Formula
  desc "Persistent AI development workstations in Docker"
  homepage "https://github.com/infrasecture/hcorral"
  license "AGPL-3.0-or-later"
  depends_on :macos

  if Hardware::CPU.arm?
    url "https://github.com/infrasecture/hcorral/releases/download/${version}/hcorral_${pkg_version}_darwin_arm64.tar.gz"
    sha256 "${arm_sha}"
  else
    url "https://github.com/infrasecture/hcorral/releases/download/${version}/hcorral_${pkg_version}_darwin_amd64.tar.gz"
    sha256 "${amd_sha}"
  end

  def install
    bin.install "hcorral"
  end

  test do
    output = shell_output("#{bin}/hcorral version")
    assert_match "hcorral ${version}", output
  end
end
EOF
}

hcorral_state_value() {
  local file="$1" key="$2"
  [[ "${key}" =~ ^[A-Z][A-Z0-9_]*$ ]] || return 2
  awk -F= -v key="${key}" '$1 == key { count++; print substr($0,length(key)+2) } END { if(count != 1) exit 1 }' "${file}"
}
