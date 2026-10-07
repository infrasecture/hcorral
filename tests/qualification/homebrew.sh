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
if brew list --formula | grep -Fxq hcorral || brew tap | grep -Fxq "$qualification_tap"; then
  echo 'ERROR: Homebrew qualification requires no existing hcorral installation or qualification tap' >&2
  exit 1
fi
brew tap-new --no-git "$qualification_tap"
cleanup() {
  if brew list --formula | grep -Fxq hcorral; then brew uninstall "$qualified_formula"; fi
  brew untap "$qualification_tap"
}
trap cleanup EXIT
formula="$(brew --repository "$qualification_tap")/Formula/hcorral.rb"
cp dist/Formula/hcorral.rb "$formula"
HOMEBREW_NO_INSTALL_FROM_API=1 brew audit --strict "$qualified_formula"
archive="$PWD/dist/hcorral_${VERSION#v}_darwin_${ARCH}.tar.gz"
# Audit the real release URL first, then install the exact unpublished archive.
# shellcheck disable=SC2016 # Ruby, not the shell, expands $_.
ARCHIVE="$archive" ARCH="$ARCH" ruby -pi -e '
  if $_.include?("darwin_#{ENV.fetch("ARCH")}.tar.gz\"")
    $_ = "    url \"file://#{ENV.fetch("ARCHIVE")}\"\n"
  end
' "$formula"
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
