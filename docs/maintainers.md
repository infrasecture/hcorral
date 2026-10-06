# Maintainer workflow

Run the source, package, shell, contract, race, and vulnerability gates with:

```console
./scripts/ci-source.sh
./build.sh --release --cli-version v0.1.0 --packages
```

`CI` uses hosted Linux amd64/arm64 and macOS Intel/Apple Silicon runners. The
release workflow prepares one artifact set, qualifies deb/rpm/Arch packages,
both Darwin archives and Homebrew formulae, and headless Colima on the Intel
macOS worker supported by Colima's own CI model, then publishes the exact
prepared bytes. Preview releases may explicitly waive unavailable
Linux X11/Wayland/XWayland evidence; stable releases require the corresponding
self-hosted runners. Docker Desktop is not a target.

The protected `release` environment needs repository-scoped
`HCORRAL_REPOSITORY_TOKEN` and tap-scoped `HCORRAL_TAP_TOKEN`. The protected
`image-release` environment publishes through `GITHUB_TOKEN` with
`packages:write`. Actions are pinned to full commits.

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

Create a launcher preview through `Release launcher` with `v0.1.0` and
`preview`. Publication updates `infrasecture/hcorral`, GitHub release assets,
and `infrasecture/homebrew-tap/Formula/hcorral.rb`, then verifies public
checksums and a fresh Homebrew installation.
