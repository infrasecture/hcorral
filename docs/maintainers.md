# Maintainer workflow

Run the source, package, shell, contract, race, and vulnerability gates with:

```console
./scripts/ci-source.sh
./build.sh --release --cli-version vX.Y.Z --packages
```

The [test design and coverage map](../tests/README.md) defines suite ownership.
Use `./tests/run.sh core`, then build once and run `./tests/run.sh linux` for
comprehensive Linux behavior. `architecture`, `compose`, `codex`, `journey` and
`compat` select independent boundaries without rerunning unrelated matrices.

CI runs comprehensive behavior on native Linux AMD64 and focused architecture
checks on Linux ARM64. Darwin AMD64/ARM64 archives are cross-built, inspected
for linkage and embedded helpers, and checked against their generated Homebrew
formula by `./tests/qualification/darwin-artifacts.sh v0.0.0`.
**There are no automated macOS runtime, Homebrew installation or Colima jobs.**
The required `ci/darwin` check represents artifact validation only. Release
records use `artifact-checked` for Darwin; Linux records require runtime passes.
The optional `tests/qualification/homebrew.sh` remains available for manual
native verification and does not publish or replace an existing installation.

Release qualification consumes one exact versioned artifact set. Preview
releases may explicitly waive unavailable Linux X11/Wayland/XWayland evidence;
stable releases require the corresponding self-hosted runners. Docker Desktop
is not a target. Historical qualification results remain in
[the implementation ledger](implementation-status.md); they do not describe the
current automated platform matrix.

The protected `release` environment needs repository-scoped
`HCORRAL_REPOSITORY_TOKEN` and tap-scoped `HCORRAL_TAP_TOKEN`. The protected
`image-release` environment publishes through `GITHUB_TOKEN` with
`packages:write`. Actions are pinned to full commits.

Merging a PR and running `CI` do not publish images. After changing the shared
image recipe, entrypoint or session setup, publish **all three streams** so each
gets the fixes. New upstream harness versions also need a new image publication.
The builder resolves the current upstream version unless `--version` is supplied;
the Dockerfile's pinned defaults are for direct Docker builds.

Publish one or all image streams with `Publish harness image`, or locally:

```console
./scripts/build-harness-image.sh --harness codex --revision auto --push
./scripts/build-harness-image.sh --harness claude --revision auto --push
./scripts/build-harness-image.sh --harness pi --revision auto --push
```

Each stream resolves its upstream version, recipe revision, source commit, and
input digest independently. Immutable architecture and manifest tags are never
replaced on conflicting identity; matching retries are idempotent. Moving
version and `latest` aliases advance monotonically. Each native architecture is
loaded locally and passes the full account, persisted-home, configuration,
session, argv, and user-prefix update canary before its immutable tag is pushed.
An idempotent retry pulls and re-runs that same canary before reusing a matching
immutable tag.

Manual publication on separate native builders remains supported. Both builders
must use the same reviewed source, harness version and recipe revision:

```console
# Run on the corresponding native architecture host.
./scripts/build-harness-image.sh --harness codex --version VERSION --revision REVISION --arch amd64 --push
./scripts/build-harness-image.sh --harness codex --version VERSION --revision REVISION --arch arm64 --push
# Finalize explicitly if both immutable architecture tags already exist.
./scripts/build-harness-image.sh --harness codex --version VERSION --revision REVISION --manifest
```

The image builder currently uses Python 3 for JSON parsing. Python 3, tmux and
less are also development prerequisites for the real terminal tests in
`ci-source.sh`. These are maintainer dependencies, not requirements for users
installing the compiled launcher. Linux binaries are built with `CGO_ENABLED=0`;
Darwin binaries use macOS system libraries without requiring an installed Go
runtime. Launcher and image publication do not depend on being performed together.

Create a launcher preview through `Release launcher` with a new `vX.Y.Z` and
`preview`. Publication updates `infrasecture/hcorral`, GitHub release assets,
and `infrasecture/homebrew-tap/Formula/hcorral.rb`, then verifies public
checksums, the native Linux archive and the published Homebrew formula's URLs
and archive digests. Homebrew installation requires the optional manual check.
