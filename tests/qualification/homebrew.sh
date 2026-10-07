#!/usr/bin/env bash
set -euo pipefail

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"
# shellcheck source=scripts/lib/release-versioning.sh
source "$root/scripts/lib/release-versioning.sh"
hcorral_require_stable_version "${VERSION:?VERSION is required}"
case "$(uname -m)" in
  arm64) arch=arm64 ;;
  x86_64) arch=amd64 ;;
  *) echo 'ERROR: unsupported Homebrew qualification architecture' >&2; exit 1 ;;
esac
[[ "$(uname -s)" == Darwin && "$arch" == "${ARCH:?ARCH is required}" ]] || {
  echo 'ERROR: Homebrew qualification requires the selected native macOS runner' >&2
  exit 1
}

(cd dist && shasum -a 256 -c SHA256SUMS)
ruby -c dist/Formula/hcorral.rb
qualification_tap=hcorral/qualification
qualified_formula="${qualification_tap}/hcorral"
if brew list --formula | grep -Fx hcorral >/dev/null || brew tap | grep -Fx "$qualification_tap" >/dev/null; then
  echo 'ERROR: Homebrew qualification requires no existing hcorral installation or qualification tap' >&2
  exit 1
fi
brew tap-new --no-git "$qualification_tap"
cleanup() {
  if brew list --formula | grep -Fx hcorral >/dev/null; then brew uninstall "$qualified_formula"; fi
  brew untap "$qualification_tap"
}
trap cleanup EXIT
formula="$(brew --repository "$qualification_tap")/Formula/hcorral.rb"
cp dist/Formula/hcorral.rb "$formula"
HOMEBREW_NO_INSTALL_FROM_API=1 brew audit --strict "$qualified_formula"
check_formula_version() {
  # shellcheck disable=SC2016 # Ruby reads its own stdin.
  HOMEBREW_NO_INSTALL_FROM_API=1 brew info --json=v2 "$qualified_formula" |
    EXPECTED_VERSION="${VERSION#v}" ruby -rjson -e '
      info = JSON.parse($stdin.read).fetch("formulae").fetch(0)
      actual = info.fetch("versions").fetch("stable")
      abort "unexpected Homebrew version: #{actual}" unless actual == ENV.fetch("EXPECTED_VERSION")
    '
}
check_formula_version
archive="$PWD/dist/hcorral_${VERSION#v}_darwin_${ARCH}.tar.gz"
# Audit the real release URL first, then install the exact unpublished archive.
# The GitHub release URL determines the original version. Preserve it explicitly
# for the temporary local URL, which Homebrew can otherwise misread as "64".
# shellcheck disable=SC2016 # Ruby, not the shell, expands $_.
ARCHIVE="$archive" ARCH="$ARCH" PACKAGE_VERSION="${VERSION#v}" ruby -pi -e '
  $_ += "  version \"#{ENV.fetch("PACKAGE_VERSION")}\"\n" if $_.start_with?("class Hcorral < Formula")
  if $_.include?("darwin_#{ENV.fetch("ARCH")}.tar.gz\"")
    $_ = "    url \"file://#{ENV.fetch("ARCHIVE")}\"\n"
  end
' "$formula"
check_formula_version
HOMEBREW_NO_INSTALL_FROM_API=1 brew install "$qualified_formula"
installed="$(brew --prefix "$qualified_formula")/bin/hcorral"
[[ "$(hcorral_sha256_file "$installed")" == "$(hcorral_sha256_file "dist/bin/hcorral-darwin-${ARCH}")" ]] || {
  echo 'ERROR: Homebrew installed a different launcher from the qualified build' >&2
  exit 1
}
"$installed" version
"$installed" --help
HOMEBREW_NO_INSTALL_FROM_API=1 brew test "$qualified_formula"
printf 'PASS: native %s Homebrew audit, installation, exact binary and formula test\n' "$ARCH"
