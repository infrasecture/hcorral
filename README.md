# Harness Corral

`hcorral` runs persistent Codex, Claude, and Pi development environments in
Docker. Each harness gets an independent container in the same physical
workspace, while the workspace and an optional persisted home can be shared.

## Install

Hcorral requires Docker and Docker Compose v2. On macOS, install the launcher
from the Infrasecture Homebrew tap:

```console
$ brew install infrasecture/tap/hcorral
$ hcorral version
```

Linux packages and standalone archives are available from the
[v0.1.0 preview release](https://github.com/infrasecture/hcorral/releases/tag/v0.1.0).
For Debian or Ubuntu on x86-64:

```console
$ curl -fLO https://github.com/infrasecture/hcorral/releases/download/v0.1.0/hcorral_0.1.0_linux_amd64.deb
$ curl -fLO https://github.com/infrasecture/hcorral/releases/download/v0.1.0/SHA256SUMS
$ sha256sum --ignore-missing --check SHA256SUMS
$ sudo apt install ./hcorral_0.1.0_linux_amd64.deb
$ hcorral version
```

The release also provides `arm64` Debian packages, `x86_64` and `aarch64` RPM
and Arch packages, and Linux/macOS archives for both architectures. Download
`SHA256SUMS` from the same release and verify a package before installing it.
See [installation](docs/installation.md) for the runtime dependency and package
details. Hcorral installs only the launcher; it does not install or modify
Docker.

## Run

```console
$ cd ~/src/payment-api
$ hcorral --harness codex
$ hcorral --harness claude
$ hcorral --harness pi
```

For `/home/alice/src/payment-api`, typical generated resources are:

```text
codex container   hcorral-payment_api-ec98cf8
claude container  hcorral-payment_api-e242908
shared home       hcorral_state
private home      hcorral-payment_api-58b272b
```

The suffixes are the first seven hexadecimal characters of full SHA-256
identities. The container hash includes the canonical physical path and harness
type; the private-volume hash includes only the path. Full hashes in
`ai.infrasecture.hcorral.*` labels, never the suffix, prove ownership.

## Selection

The harness defaults to `codex`. Select an image independently when needed:

```console
hcorral --harness claude
hcorral --harness claude --image registry.example/ai/claude:approved
hcorral --harness company_agent --image registry.example/ai/agent@sha256:...
```

Harness precedence is CLI, `HCORRAL_HARNESS`, user config, then `codex`. Image
precedence is CLI, `HCORRAL_IMAGE`, the selected user-config entry, then that
harness's built-in `:latest` image. See [configuration](docs/configuration.md).

`--project-name experiment-a` is an intentional escape hatch for running a
second independent instance of the same harness against the same workspace.
Commands using the override target that exact project; commands without it
target only the generated project. Hcorral warns when it observes this
multiplicity.

## State and deletion

The default persisted home is the global external volume `hcorral_state`.
`--private-env` selects the workspace-private volume, and `--state-volume NAME`
selects a user-managed custom volume.

`hcorral down` removes only the selected Compose project. `hcorral down -v`
also removes the selected workspace-private volume. If its complete ownership
labels do not match, or another running or stopped container references it,
hcorral refuses the request before tearing down the selected project and names
the blocker. Remove the other corrals first, using plain `down` where their
shared state must remain, then retry `down -v`. It never removes `hcorral_state`
or a custom volume. Orphan cleanup is explicit:

```console
hcorral state rm --scope workspace
hcorral state rm --scope global
```

Both commands refuse a referenced volume or one without exact ownership
labels.

## Images and updates

Built-ins are independent multi-architecture streams:

```text
ghcr.io/infrasecture/hcorral-codex:<codex-version>-rN
ghcr.io/infrasecture/hcorral-claude:<claude-version>-rN
ghcr.io/infrasecture/hcorral-pi:<pi-version>-rN
```

`hcorral pull` only fetches the selected reference. `hcorral up -d` explicitly
reconciles the container. Bare launch attaches to an already-running container
without pulling or recreating it. Update checks are bounded, informational, and
disabled with `HCORRAL_UPDATE_CHECK=false`.

Manual in-container updates are allowed and persisted-user paths precede image
tools. Recreating a container restores the selected image layer while retaining
mounted state and workspace data.

## GUI and Compose overlays

Headless mode works with the selected Docker context, including Colima and
deliberately configured remote daemons. GUI forwarding is Linux-only and
requires a local daemon:

```console
hcorral --gui=x11
hcorral --gui=wayland
hcorral --no-gui
```

The embedded base Compose file is always first. `-f FILE` overlays and `-v`
mounts are trusted, unrestricted Docker inputs and may replace any built-in
safety property, image, label, mount, or service. Hcorral reports the final
rendered/deployed result; it does not sanitize overlays or sidecars.

## Existing myCodex environments

Hcorral is a separate project. If it verifies a running or stopped myCodex
container for the same workspace, operational commands exit without mutation.
Use myCodex to attach or run `myCodex down` before starting hcorral. Hcorral
does not migrate, adopt, relabel, or delete myCodex resources.

## Development

```console
./scripts/ci-source.sh
./build.sh --release --cli-version v0.1.0 --packages
./scripts/build-harness-image.sh --harness codex --version 0.149.1 --revision 1
```

Launcher releases and each harness image stream are versioned independently.
The project is licensed under `AGPL-3.0-or-later`; direct dependency notices are
in [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).
