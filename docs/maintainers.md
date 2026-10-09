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

GitHub Actions is the normal release path; `release.sh` is for local use only.
The launcher workflow uses GitHub's automatically provided `GITHUB_TOKEN` with
`contents:write`; the image workflow uses it with `packages:write`. Neither needs
a personal token or a custom repository secret. Each workflow has **one**
environment approval gate (`release` or `image-release`). The image gate covers
all resolved streams, native builds, and manifests. Actions are pinned to full
commits and publication is restricted to `main`.

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

The image builder currently uses Python 3 for JSON parsing. Python 3, jq, tmux
and less are development prerequisites for the real terminal tests in
`ci-source.sh`. These are maintainer dependencies, not requirements for users
installing the compiled launcher. Linux binaries are built with `CGO_ENABLED=0`;
Darwin binaries use macOS system libraries without requiring an installed Go
runtime. Launcher and image publication do not depend on being performed together.

Create a launcher preview, approve its publication once, then update Homebrew:

```console
gh workflow run release.yaml --repo infrasecture/hcorral -f version=vX.Y.Z -f channel=preview
# After Release launcher succeeds:
gh workflow run update-hcorral.yaml --repo infrasecture/homebrew-tap -f version=vX.Y.Z
```

The launcher workflow builds and qualifies one exact set of archives and Linux
packages, then publishes and verifies the GitHub release. The tap workflow
downloads the public Darwin archives, checks their digests, and updates the
formula using **its own** repository's `GITHUB_TOKEN`. No credential crosses
repository boundaries. Updating the `homebrew-tap` submodule pointer afterward
is an ordinary repository change through a PR, not part of package publication.
Homebrew installation still requires the optional manual check.

If publication fails after qualification, resume the exact prepared run:

```console
gh workflow run release.yaml --repo infrasecture/hcorral -f version=vX.Y.Z -f channel=preview -f prepared_run_id=RUN_ID
```

Recovery checks the original main-branch workflow identity, successful
qualification jobs, artifact ownership, expiry and ZIP digest, recorded source,
channel and file hashes. It never rebuilds. Existing annotated tags and public
assets must match; only missing assets may be uploaded. Use the original build
run ID within its 30-day artifact retention period. Expired or conflicting
evidence requires investigation; do not overwrite a published version.
