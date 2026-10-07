# Native qualification

`codex-sessions.sh` runs the built `dist/tests/session-core-<platform>` executable
against two pinned official Codex releases on the runner's actual architecture.
Build these executables with `./build.sh --release --cli-version v0.0.0`; they are
CI evidence tools and are not included in the launcher archives/packages.
`HCORRAL_CODEX_TEST_BINARY` selects a separately built native test executable.

The checksum manifest is `tests/fixtures/codex-releases.tsv`. Digests were read
from the official [0.160.0](https://github.com/openai/codex/releases/tag/rust-v0.160.0)
and [0.160.1](https://github.com/openai/codex/releases/tag/rust-v0.160.1) release
assets. Downloads are verified before extraction/execution. Updating the matrix
requires reviewing format, indexing and writer-lock compatibility as well as
replacing version/digest entries; it must not silently track `latest`.

The suite runs in both version directions with disposable homes/workspaces and
an in-process loopback provider. It checks the native picker, resume and writer
exclusion, a completed turn followed by transfer/resume in the other version,
archive and compressed inherited history, compatible prerequisite growth,
complete-parent promotion/new forks, concurrent initial indexing and effective
SQLite configuration. The same executable also runs the core safety, conflict,
ownership and process-crash recovery tests. No user credentials or hosted model
service are required. This is native filesystem/runtime qualification; real
Docker endpoint tests live in `tests/integration`.

The integration runner also calls this script with `HCORRAL_NATIVE_DOCKER=1`,
an explicit `HCORRAL_SESSION_TEST_IMAGE` and the packaged `HCORRAL_TEST_BINARY`.
That mode runs `TestCodexResumesNativeHistory` through the public commands:
export synthetic container history, resume on the native host, complete a turn
against the loopback provider, import the native-written result into another
stopped container, export it again and resume with the peer Codex version.
Legacy/paginated histories, compressed inherited prefixes, revert selection,
archive state and existing indexed destinations retain the same context/privacy
assertions as the core test. Both version directions run. The fixture volumes
use the source's absolute pathname inside a separate container filesystem so
their synthetic SQLite selections remain valid; they are never host binds.
Fixture setup is separate from the public transfer's selective-copy contract.
Each public operation must preserve the stopped workstation and remove its
helper. This composition gate requires real Docker execution and is not implied
by success of the ordinary in-process native suite.

CI runs the native session suite on Linux/macOS AMD64/ARM64 and real Colima
integration on both macOS targets. On ARM64, Colima uses an x86_64 QEMU guest
to avoid depending on nested hardware virtualization. The launcher and session
test executable still run natively as ARM64 macOS programs, selecting the
embedded AMD64 helper for the actual Linux container. This also exercises
different host/container architectures. It is not a native ARM64 guest-VM test;
the Linux ARM64 jobs separately execute that container/helper architecture.
The ARM host also needs Homebrew's `lima-additional-guestagents` package for
the x86_64 guest agent, in addition to QEMU.
See Colima's documented [architecture selection](https://colima.run/docs/configuration/).
This added hosted ARM64 Docker gate still requires a successful run.
The release workflow uses the same session gates before publication can proceed.
Running the ordinary CI workflow never publishes a launcher or image.

`mixed-versions.sh` qualifies the actual published v0.1.0 launcher against the
new launcher artifact, each with an image built from the pre-parity recipe at
`18fb3944f9b04758b920b1e9415fd017eff8cf51` and the current recipe. Both images
contain pinned Codex 0.160.0, isolating the launcher/image contract from changes
to Codex itself. The old launcher archive is checksum-verified before use.
Both image builds run their normal entrypoint canaries; the new image also
runs the full persisted-home suite. Three UID/GID pairs share a fresh home
between concurrently running old/new image containers, then exercise new panes
in the older container after the newer image installs compatible startup files.

The four launcher/image combinations use isolated workspaces and state volumes.
Actual PTY attach/detach and retained/reopened reports are checked against the
runtime user's tmux server. Container identity, lifecycle timestamps, mounts,
panes and a persistent-state sentinel must survive attachment. This test does
not promise that a new launcher repairs an older image's entrypoint. CI and
release qualification run it on native Linux AMD64/ARM64 with the built launcher
(the extracted DEB binary during release qualification). No image is pushed,
and no existing workstation is adopted. Builder/test tools, including Python
for PTY driving, are qualification dependencies, not launcher prerequisites.

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
mounts on a suitable Linux desktop host. It is separate from unit GUI discovery
tests and headless Docker acceptance. Merely defining a workflow or having a
test file does not establish that these gates passed for a release.

`hosted-gui.sh` supplies real local Xvfb, Weston and XWayland servers on the
Linux qualification runners. It derives a test image from the built production
Codex image, adding only `xset`/`wayland-info` diagnostic clients; the entrypoint,
UID/GID handling, shell initialization and tmux remain unchanged. The protocol
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
