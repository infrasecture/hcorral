# Maintainer guide

## Required checks

```bash
./scripts/ci-source.sh
./build.sh --release --cli-version v0.1.0 --packages
HCORRAL_TEST_BINARY="$PWD/dist/bin/hcorral-linux-$(uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')" \
  ./tests/integration/run.sh
```

All Go work runs in the pinned builder image. Image, launcher, package, and
Homebrew publication is performed by GitHub workflows. Third-party actions are
pinned to full commits and publication credentials exist only in protected
environments.

`scripts/check-provenance.sh` fixes the myCodex, Vaka, and implementation-start
Homebrew-tap baselines and the copied/adapted-file inventory. The direct
software inventory and fixed optional-agent versions are checked separately by
`scripts/check-third-party.sh`.

CI runs the real-Docker contract with both the runner's current Compose v2 and
the checksum-pinned lowest-supported v2.24.6 standalone binary. Raising that
floor is a reviewed compatibility decision, not an incidental runner upgrade.

## Launcher release

```bash
./release.sh --version v0.1.0 --channel preview --prepare-only
./release.sh --version v0.1.0 --channel preview --publish-prepared
```

Preparation is non-publishing and records exact artifact hashes under ignored
`dist/release-state/`. Publication never rebuilds. Public releases normally use
`.github/workflows/release.yaml`, including Linux package, macOS/Homebrew, and
Docker Desktop qualification.
The protected `release` environment must contain two fine-grained secrets:
`HCORRAL_REPOSITORY_TOKEN` has contents-write access only to
`infrasecture/hcorral` and uses the repository ruleset's explicit owner bypass;
`HCORRAL_TAP_TOKEN` has contents-write access only to
`infrasecture/homebrew-tap`. The default workflow token remains read-only.
The official Arch Linux container is amd64-only, so its qualification installs
through `pacman` on amd64. The native arm64 job instead extracts the Arch package
into an isolated root, verifies its metadata, paths, and modes, and executes the
exact packaged binary. This keeps the arm64 gate useful without trusting an
unofficial base image.

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
