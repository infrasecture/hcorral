# Harness Corral

`hcorral` runs persistent Codex, Claude, and Pi development environments in
Docker. Each harness gets an independent container in the same physical
workspace, while the workspace and an optional persisted home can be shared.

The installation commands below select the `v0.2.0` preview, including automatic
GUI discovery, inactive image refresh, retained notices and session transfer.
Shared shell defaults are included in the refreshed workstation images. See the
[implementation ledger](docs/implementation-status.md) for qualification results;
updating a source checkout does not update an installed launcher or running image.

## Install

Hcorral requires Docker and Docker Compose v2, installed separately.

### Linux

Linux is the primary target. DEB, RPM, Arch Linux, and standalone archives for
AMD64 and ARM64 are available from the
[v0.2.0 preview release](https://github.com/infrasecture/hcorral/releases/tag/v0.2.0).
Download `SHA256SUMS` once, then choose the package for your distribution. For
x86-64 Linux:

```console
$ curl -fLO https://github.com/infrasecture/hcorral/releases/download/v0.2.0/SHA256SUMS

# Debian or Ubuntu
$ curl -fLO https://github.com/infrasecture/hcorral/releases/download/v0.2.0/hcorral_0.2.0_linux_amd64.deb
$ sha256sum --ignore-missing --check SHA256SUMS
$ sudo apt install ./hcorral_0.2.0_linux_amd64.deb

# Fedora, RHEL, or another RPM-based distribution
$ curl -fLO https://github.com/infrasecture/hcorral/releases/download/v0.2.0/hcorral-0.2.0-1.x86_64.rpm
$ sha256sum --ignore-missing --check SHA256SUMS
$ sudo dnf install ./hcorral-0.2.0-1.x86_64.rpm

# Arch Linux
$ curl -fLO https://github.com/infrasecture/hcorral/releases/download/v0.2.0/hcorral-0.2.0-1-x86_64.pkg.tar.zst
$ sha256sum --ignore-missing --check SHA256SUMS
$ sudo pacman -U ./hcorral-0.2.0-1-x86_64.pkg.tar.zst
```

For ARM64, select the release asset containing `arm64` for DEB or `aarch64` for
RPM and Arch. Linux archives provide a package-manager-independent alternative.
After installation, run `hcorral version`.

### macOS

The launcher supports headless operation on macOS. Install it from the
Infrasecture Homebrew tap:

```console
$ brew install infrasecture/tap/hcorral
$ hcorral version
```

See [installation](docs/installation.md) for runtime details. Hcorral installs
only the launcher; it does not install or modify Docker.

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

For inactive projects, bare launch refreshes `latest` by default. A stopped
container is replaced only if its image changed and Compose can reproduce its
deployed configuration; otherwise its original image and mounts are preserved.
An offline registry also preserves the stopped container. Set
`HCORRAL_AUTO_PULL=false` to disable automatic refresh. Named tags and digests
remain pinned. See the complete [runtime policy](docs/runtime-model.md).

Startup and update reports remain available inside tmux. Dismiss the scrollable
report with `q`, and reopen it later with `hcorral notices`. A session badge
shows the deployed GUI mode.

Manual in-container updates are allowed and persisted-user paths precede image
tools. Recreating a container restores the selected image layer while retaining
mounted state and workspace data.

## Codex session transfer

Session transfer is implemented on the development branch and still undergoing
Docker/runtime and consistency qualification. See the remaining gates in the
[implementation ledger](docs/implementation-status.md) before using it with
important conversations.

```console
hcorral session export <session-id>
hcorral session import <session-id> /path/to/host/codex-home
hcorral session --help
```

Export copies from the selected Codex corral to the host; import reverses that
direction. The optional path names the host Codex home itself and defaults to
host `CODEX_HOME`, then `~/.codex`. Relative paths use the caller's directory.
The host is the Docker client machine, including when Docker uses a remote daemon.
An existing running or stopped corral is required; transfer does not pull images,
start/recreate the workstation, attach to tmux or resume Codex.

The command copies the selected native history and required inherited prefixes.
It preserves the session ID and reuses identical existing files; divergent
history is a conflict. A managed inherited prefix can grow when a later fork
needs more matching history; existing children keep their original boundaries.
Importing the complete parent later makes that parent independently resumable.
If Codex indexed a prerequisite, the importer repairs that conversation's selected
path in a supported destination database while preserving existing names and
unrelated metadata. This repair currently supports Codex 0.160.0/0.160.1's schema.
Credentials, configuration, workspace files, database-only names/metadata and
external resources are excluded. **The source conversation can stay open and keep
running.** Copying captures the complete records already saved at a fixed boundary;
it excludes an unfinished final record, pending writes and later messages. The
original continues independently. The copy keeps its session ID; this is a
snapshot, not synchronization between two conversations. Existing conversations
at the destination are never overwritten, and a busy destination is refused.
Review saved workspace paths and permissions before resuming the copy. Archived
sessions stay archived.

Native format and destination-lock checks cover Codex 0.160.0/0.160.1. Source
copying does not acquire Codex's writer lock or require a writable source home.
Destination publication still requires working locks and a supported database
layout. Shared filesystems must coordinate those locks across clients. Transfers
reject FUSE (including virtiofs/SSHFS), 9p, NFS and SMB storage on Linux, and
non-local or FUSE/virtiofs/9p storage on macOS. In particular, a Mac directory
bind-mounted into Colima is not supported conversation storage: guest locks
can succeed while a host writer owns the same lock. Keep the corral's Codex
home in a daemon-local volume and export/import to the native host home instead.
This restriction applies to nested history, lock and SQLite storage too; there
is no bypass flag. Other unqualified storage still requires review. See the
[remaining consistency boundaries](docs/session-transfer-design.md#remaining-consistency-questions)
and platform evidence before treating a different runtime/storage combination as
supported.

SQLite metadata may live outside `CODEX_HOME`. Discovery reads local base config,
local requirements and `CODEX_SQLITE_HOME` separately at each endpoint. To select
the effective directory when Codex uses project config, CLI-selected profiles,
runtime flags, cloud policy or macOS managed preferences, use:

```console
hcorral session export <session-id> --host-sqlite-home /host/state --container-sqlite-home /container/state
```

Explicit container paths must be absolute. A separate SQLite directory must
already exist; container metadata must be in persistent mounted storage.
`--format=json` returns the confirmed result and resolved database locations.
A result can accompany a nonzero exit if publication succeeded but later helper
cleanup failed. A missing result is not proof that the destination was unchanged;
inspect it or retry, which reuses verified identical history and completes any
pending selection repair. A failed transfer can retain fully copied prerequisite
files; finish the import before using the requested conversation. A retry also
cleans recognized abandoned staging after a killed transfer, while preserving
active transfers and published history.

Complete builds bundle Linux AMD64 and ARM64 helpers, so users need neither Go
nor Python installed. Use `build.sh` for a complete source build; plain `go build`
without generated helper assets cannot perform transfers. Advanced size limits
and the detailed endpoint behavior are documented in `hcorral session --help` and
the [transfer design](docs/session-transfer-design.md).

## GUI and Compose overlays

Headless mode works with the selected Docker context, including Colima and
deliberately configured remote daemons. GUI forwarding is Linux-only and
requires a local daemon:

```console
hcorral --gui=x11
hcorral --gui=wayland
hcorral --no-gui
```

New Linux environments automatically prefer usable Wayland, then X11, with a
headless fallback. Automatic forwarding is disabled over SSH and unsupported
daemons; macOS remains headless. Explicit GUI requests report unavailable access.
An existing container keeps its mode until explicit reconciliation.

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
./build.sh --release --cli-version v0.2.0 --packages
./scripts/build-harness-image.sh --harness codex
```

Launcher releases and each harness image stream are versioned independently.
The project is licensed under `AGPL-3.0-or-later`; direct dependency notices are
in [THIRD_PARTY_LICENSES.md](THIRD_PARTY_LICENSES.md).

Automated runtime tests cover Linux AMD64 and ARM64. Darwin archives are cross-built
and inspected; macOS execution and Homebrew installation are not currently tested
in CI. See the [test coverage map](tests/README.md).
