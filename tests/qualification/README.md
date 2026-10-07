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

CI runs the native session suite on Linux/macOS AMD64/ARM64 and real Colima
integration on macOS Intel. The hosted macOS ARM64 job runs native core and
launcher checks but does not establish a local Docker/VM acceptance result.
The release workflow uses the same session gates before publication can proceed.
Running the ordinary CI workflow never publishes a launcher or image.

`linux-gui.sh` qualifies actual X11 or Wayland forwarding and narrow socket
mounts on a suitable Linux desktop host. It is separate from unit GUI discovery
tests and headless Docker acceptance. Merely defining a workflow or having a
test file does not establish that these gates passed for a release.
