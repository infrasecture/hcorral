# Native qualification

`codex-sessions.sh` runs the built `dist/tests/session-core-<platform>` executable
against two pinned official Codex releases on the runner's actual architecture.
Build the Linux executables with `./build.sh --release --cli-version v0.0.0`; they are
CI evidence tools and are not included in the launcher archives/packages.
`HCORRAL_CODEX_TEST_BINARY` selects a separately built native test executable.

The checksum manifest is `tests/fixtures/codex-releases.tsv`. Digests were read
from the official [0.160.0](https://github.com/openai/codex/releases/tag/rust-v0.160.0)
and [0.160.1](https://github.com/openai/codex/releases/tag/rust-v0.160.1) release
assets. Downloads are verified before extraction/execution. Updating the matrix
requires reviewing format, indexing and writer-lock compatibility as well as
replacing version/digest entries; it must not silently track `latest`.

Within a CI job, verified release archives are reused from a private directory
under `RUNNER_TEMP`. They are keyed by the pinned SHA-256 and checked again before
every extraction; partial downloads are never installed in the cache. This lets
the later Docker journeys reuse the native suite's assets without another
GitHub download. Outside CI the cache is temporary unless
`HCORRAL_CODEX_TEST_CACHE` selects a persistent test-asset directory. Only public
archives are cached; executables are extracted afresh and synthetic Codex homes
are private to each invocation.

The suite runs in both version directions with disposable homes/workspaces and
an in-process loopback provider. It checks the native picker, resume and writer
exclusion, a completed turn followed by transfer/resume in the other version,
archive and compressed inherited history, compatible prerequisite growth,
complete-parent promotion/new forks, concurrent initial indexing and effective
SQLite configuration. Version-independent safety, conflict, ownership and
process-crash recovery tests run once in the source suite. No user credentials or hosted model
service are required. This is native filesystem/runtime qualification; real
Docker endpoint tests live in `tests/integration`.

Native fixtures disable plugin startup, which would otherwise fetch unrelated
catalogs from the network. Each app server owns a separate process group so its
background children are stopped before disposable homes are removed, including
after cancellation. Live-copy fixtures keep the source loaded during transfer.
A separate fixture holds a model turn open until after copying, then proves that both the original
and the resumed copy remain usable.

The native revert fixture also completes two actual turns, reverts before the
second and transfers the native-created replacement. It requires the removed
continuation to be absent from both exported bytes and resumed provider context,
and copies successfully before and after revert while the native source stays
loaded. The separate lifecycle cases check archive/unarchive/resume/delete
refusal while a destination publication guard is held, then successful retry
after release.
Further native fixtures cover Git metadata, compression and background format
migration. Unrelated cold/legacy files must be processed to establish that the
maintenance workers actually ran, while guarded history stays unchanged.
After release, the same selected history must compress/migrate and remain
transferable. These cases do not qualify pre-protocol writers or arbitrary
network filesystem semantics.

Public Docker journeys are selected explicitly by `codex-sessions.sh journey`.
They run `TestDockerCodexRoundTrip` and `TestDockerCodexActiveTurn` in both version
directions. The ordinary native suite selects only Codex-specific tests, so it
does not repeat the version-independent core. See [the coverage map](../README.md)
for the native, process, Docker, image and distribution boundaries.

CI and release execute Linux only. Darwin binaries are cross-built and inspected;
`darwin-artifacts.sh` checks archives, architecture, licenses and formula digests.
These checks are not Homebrew installation or native filesystem qualification.
Historical Colima evidence does not imply current macOS execution.

`homebrew.sh` is available for optional manual verification on macOS. It is not
an automated CI or release gate. The full nonpublishing release build generates the
formula from its two Darwin archives; partial target builds do not combine a new
archive with a stale archive from another build. The gate verifies checksums,
audits the release URL in a local tap, then substitutes only the selected URL
with the local unpublished archive. Homebrew verifies its original SHA-256,
installs it, and runs the formula test. The installed executable must also match
the build's binary exactly. The gate removes its package and tap and refuses an
already installed hcorral or existing qualification tap. Generation/source checks
alone do not establish that Homebrew installation passed on either architecture.

`compatibility.sh` prepares the pinned old/new artifacts once. Its separately
reported shared-home, launcher/image, version-probe, hosted-GUI and migration
steps consume those fixtures. `mixed-versions.sh` qualifies the actual published v0.1.0 launcher against the
new launcher artifact, each with an image built from the pre-parity recipe at
`18fb3944f9b04758b920b1e9415fd017eff8cf51` and the current recipe. The historical
image contains pinned Codex 0.160.0; the current image uses pinned 0.160.1.
The old launcher archive and Codex archives are checksum-verified before use.
Fixture preparation does not rerun image publication canaries: the separate
image jobs own those matrices. Three UID/GID pairs share a fresh home
between concurrently running old/new image containers, then exercise new panes
in the older container after the newer image installs compatible startup files.

The four launcher/image combinations use isolated workspaces and state volumes.
Actual PTY attach/detach and retained/reopened reports are checked against the
runtime user's tmux server. Container identity, lifecycle timestamps, mounts,
panes and a persistent-state sentinel must survive attachment. This test does
not promise that a new launcher repairs an older image's entrypoint. CI and
release qualification run it once on native Linux AMD64 with the built launcher
(the extracted DEB binary during release qualification). No image is pushed,
and no existing workstation is adopted. Builder/test tools, including Python
for PTY driving, are qualification dependencies, not launcher prerequisites.

The same production image then runs `version-probe.sh`. An ordinary version
probe must identify its bundled executable before the fixture introduces a
blocked login profile and a blocked user-prefix executable. Both deliberately
ignore TERM and spawn a child. The public `info` command must return within its
outer deadline, fall back to the bundled version, and leave no monitor, shell
or child process behind. Restoring the fixture startup files must restore normal
version discovery without changing workstation identity, image or mounts.
It also checks that the production image's runtime process retains every host
supplementary group. The common Docker lifecycle test independently compares
the packaged launcher's actual container UID/GID/group environment against
the host process on both Linux architectures. This supplements the
static Linux unknown-account unit test and image account-mapping matrix.

The same job runs `mycodex-transition.sh` against its newly built Codex image.
It fetches immutable myCodex source and builds the real historical image recipe
with Codex 0.160.0, disabling optional unrelated agents. The original launcher
creates/removes the old project; hcorral creates a separate project with an
explicitly copied or reused state volume. It checks refusal while the old
container exists, startup customization, numeric ownership, symlinks, volume
labels, fake credentials and a synthetic conversation, including native picker
and resume through both images. It returns through the original launcher before
discarding its own fixtures. See the [manual transition procedure](../../docs/transition-from-mycodex.md).

The copied-home scenario also invokes public `session export`, resumes the
exported conversation in a native host Codex executable from the actual image,
imports it through public `session import` into a stopped separate workstation,
and starts/resumes it there. The import must preserve that stopped container's
identity, status and mounts. This connects the actual Docker transport to native
resume for a simple real-indexed history; the separate core suite covers more
complex inherited, archived and reverted histories. All authentication is
synthetic, provider configuration is local, and no model turn is started.

`linux-gui.sh` qualifies actual X11 or Wayland forwarding and narrow socket
mounts on a suitable Linux desktop host. It requires a previously qualified
production Codex image as its second argument. It derives a disposable image
adding only diagnostic clients, checks the actual invoking UID and uses that
user's tmux server. There is no fallback to the minimal root-only fixture.
Both this script and `hosted-gui.sh` use this same production-image path.

For a physical desktop check, run from a terminal in the logged-in desktop
session with a local native Linux Docker engine and Compose. Keep its real
`DISPLAY`, `WAYLAND_DISPLAY`, `XDG_RUNTIME_DIR` and Xauthority environment;
do not manufacture display variables or remove SSH markers to obtain a pass.
The host needs Docker/Buildx, Bash, Python 3 for PTY driving and the image
builder, and `xauth` for X11. These are qualification tools, not new launcher
runtime requirements. The launcher under test must be an extracted release or
CI artifact, not an unrelated installed binary.

The following nonpublishing procedure builds the reviewed image recipe with
pinned Codex, runs its normal image canaries, and checks Wayland. Change `mode`
to `x11` for a real X11 session or XWayland on a Wayland desktop:

```bash
(
  set -euo pipefail
  mode=wayland
  case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    aarch64) arch=arm64 ;;
    *) echo 'unsupported desktop architecture' >&2; exit 2 ;;
  esac
  export HCORRAL_TEST_BINARY="$PWD/dist/bin/hcorral-linux-$arch"
  test -x "$HCORRAL_TEST_BINARY"
  test_root="$(mktemp -d /tmp/hcorral-desktop.XXXXXX)"
  image_repository="hcorral-desktop-$(basename "$test_root" | tr '[:upper:]' '[:lower:]')"
  image="$image_repository:0.160.0-r1-$arch"
  trap 'docker image rm "$image" "$image_repository:0.160.0-r1" >/dev/null 2>&1 || true; rmdir "$test_root"' EXIT
  HCORRAL_IMAGE_REPOSITORY="$image_repository" ./scripts/build-harness-image.sh \
    --harness codex --version 0.160.0 --revision 1 --arch "$arch"
  git rev-parse HEAD
  sha256sum "$HCORRAL_TEST_BINARY"
  docker image inspect --format '{{.Id}} {{.Architecture}}' "$image"
  ./tests/qualification/linux-gui.sh "$mode" "$image"
)
```

Record the source revision, launcher hash, image identity, desktop/compositor
version, architecture, UID/GID and complete result with the qualification
evidence. Run each required desktop mode separately; a Wayland pass does not
imply an XWayland or X11 pass. The script retains desktop settings, creates its
own workspace and private state volume, and removes its own fixtures. It does
not publish images, alter desktop configuration or adopt a user workstation.
The stable release workflow builds the same production recipe before calling
this script on its corresponding self-hosted desktop runner.

This is separate from unit GUI discovery tests and headless Docker acceptance.
Merely defining a workflow or having a test file does not establish that these
gates passed for a release.

`hosted-gui.sh` supplies real local Xvfb, Weston and XWayland servers on the
Linux qualification runners. It passes the built production Codex image to the
shared `linux-gui.sh` procedure, adding only `xset`/`wayland-info` diagnostic
clients; the entrypoint, UID/GID handling, shell initialization and tmux remain
unchanged. The protocol
clients execute as the actual host UID inside the container, rather than the
minimal fixture's root-only stub. No host desktop configuration is modified.

It checks automatic X11 and Wayland selection, Wayland preference when both
servers are available, authenticated XWayland access, narrow read-only mounts,
the deployed GUI badge after real PTY attachment, preserved attachment identity
after display variables disappear, and refusal of an unusable explicit GUI
request without replacing the existing container. Server processes, credentials,
workspaces and images are scoped to that invocation and cleaned up afterwards.
X11 access uses a fresh authentication cookie, not unrestricted `xhost` access.

Weston runs its headless backend with software rendering; XWayland uses shared
memory in its supported standalone testing mode. See the platform's
[Weston manual](https://manpages.ubuntu.com/manpages/noble/man1/weston.1.html) and
[XWayland manual](https://manpages.ubuntu.com/manpages/noble/man1/Xwayland.1.html).
These are native protocol/permission tests, not physical display, GNOME/KDE
clipboard or GPU-driver qualification. Existing stable-release desktop gates
remain separate; no absence of a registered desktop runner is treated as a pass.
