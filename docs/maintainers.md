# Maintainer guide

## Required checks

```bash
./scripts/ci-source.sh
./build.sh --release --cli-version v0.1.0 --packages
HCORRAL_TEST_BINARY="$PWD/dist/bin/hcorral-linux-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')" \
  ./tests/integration/real-docker.sh
```

All Go work runs in the pinned builder image. Image, launcher, package, and
Homebrew publication is performed by GitHub workflows. Third-party actions are
pinned to full commits and publication credentials exist only in protected
environments.

## Launcher release

```bash
./release.sh --version v0.1.0 --channel preview --prepare-only
./release.sh --version v0.1.0 --channel preview --publish-prepared
```

Preparation is non-publishing and records exact artifact hashes under ignored
`dist/release-state/`. Publication never rebuilds. Public releases normally use
`.github/workflows/release.yaml`, including Linux package, macOS/Homebrew, and
Docker Desktop qualification.

## Workstation image release

```bash
./scripts/build-workstation-image.sh --version 0.147.0 --revision auto
```

Architecture tags are immutable. A complete matching amd64/arm64 set is
required before the immutable manifest and monotonic aliases are created.
`.github/workflows/publish-workstation-image.yaml` is the normal publisher.

## Dependency updates

Pinned builder, package, base-image, and workflow action revisions change only
in reviewed commits with source, reproducibility, package, and runtime gates.
Do not skip vulnerability checks for a public release.
