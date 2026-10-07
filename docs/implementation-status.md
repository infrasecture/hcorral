# Go successor implementation

Implementation of the accepted 2026-10-05 proposal in myCodex
`.proposals/hcorral-go-successor.md`, based on hcorral `18fb394` and myCodex
`ebc930a`. This is a working acceptance ledger, not a declaration that the
proposal is complete. All five phases, including session transfer, remain in
scope. No image publication or migration of existing user state is part of
development testing.

## Acceptance ledger

| Requirement | State | Evidence / remaining work |
| --- | --- | --- |
| Updated behavior, ADRs and parity baseline | Updated | Runtime/configuration docs, ADR 0004, provenance and manifest now reference #17/#18; Go contract tests pass |
| Shared shell defaults and preserved existing homes | Implemented; runtime pending | Atomic user-owned initialization; ShellCheck passes; real UID/GID matrix wired into native image canaries but not run locally |
| GUI badge and retained/reopenable tmux reports | Implemented; image matrix pending | Launcher-embedded helper, deployed identity/home/session, `notices`; real PTY and handshake tests pass |
| Automatic GUI with headless, SSH and remote fallback | Implemented; native desktop pending | Resolver tests cover SSH, daemon/context precedence, Desktop rejection and no credential writes during discovery |
| Inactive `latest` refresh and guarded stopped replacement | Implemented; Docker pending | State-transition tests pass; `tests/integration/image-refresh.sh` added to integration suite, execution still required |
| Actual deployed image identity | Implemented | Docker `Image` ID retained separately from configured reference; mutable-alias and bundled/installed-version tests pass |
| Numeric process identity and groups | Implemented; other platforms pending | Static Linux test executable passed as 1000:1000, 501:20 and 12345:23456, each with supplementary group 44444; no host accounts created |
| Bounded probes and readiness | Implemented; image cleanup pending | Docker/Compose capture contexts, outer readiness deadline, and container-side timeout; real container process cleanup remains to verify |
| Read-only discovery | Implemented | Separate GUI Discover/Prepare paths; credential-inode preservation test; Compose cache effects documented |
| Static binaries and packages on four targets | Partial | Four CGO-disabled cross-builds and linkage checks pass; exact release packages, native ARM64/macOS execution and later helper footprint remain |
| Manual image build workflow | Preserved; publication not performed | Separate native architecture workflow documented; builder Python dependency distinguished from launcher prerequisites; publication contract tests pass |
| Mixed launcher/image versions | Pending | Actual old/new artifacts and shared-home compatibility |
| State-preserving myCodex transition | Pending | Preserve legacy guard; test explicit volume reuse procedure |
| Session format discovery and dependencies | Core implemented; broader qualification pending | Rooted inspection; legacy/paginated, archive/Zstandard, authoritative revert selection and exact inherited prefixes; native 0.160.0/0.160.1 fixtures and completed-turn re-export pass |
| Session consistency and conflicts | Core, extension, promotion, index repair and staging recovery implemented; qualification remains | Read-only source selection; writer guards; compatible prefix growth/promotion; native initial-index overlap; actual SIGKILL recovery preserves active staging and published history; broader writer/runtime/filesystem qualification remains |
| Session endpoints and helper distribution | Implemented; broader qualification pending | Public commands, base file/env SQLite discovery with explicit overrides, inspected storage/identity, streaming and bundled helpers; all four development launchers contain both exact payloads; native endpoint/package matrix remains |
| Session transfer lifecycle | Implemented transport; Docker pending | Disposable helper uses the deployed image/storage and bypasses workstation startup; ownership/cancellation/failure tests use a controlled Docker runner, not a live daemon |
| Actual Codex resume acceptance | Partial | Core transfer/resume and native-written history pass between 0.160.0 and 0.160.1 in both directions, fresh/initialized homes; Docker endpoints, broader fixtures and platform matrix remain |

## Execution environment

Initial local checkout has no Go installation or Docker CLI/daemon. A verified
Go 1.25.13 toolchain was downloaded outside the repository for local Go tests.
User namespaces are unavailable (`unshare --user` is denied). Local tmux is
available. Container and native macOS gates must be run in suitable execution
environments; source inspection or mocks do not substitute for those gates.

## Local checkpoint: 2026-10-06

- `go test ./...`, `go vet ./...`, and `go test -race ./...` passed with the
  temporary verified Go 1.25.13 toolchain.
- SemVer fuzzing passed its 25,000-iteration budget.
- ShellCheck and Bash syntax checks passed for the source/test scripts.
- Provenance, dependency-license, image-versioning and launcher-release
  command-contract checks passed.
- `go mod tidy -diff` was clean and the pinned vulnerability scan found no
  vulnerabilities. Workflow lint exposed two preexisting ShellCheck false
  positives (environment variable naming and quoted Ruby); after two scoped
  annotations the workflow lint and release-contract checks passed.
- `tests/tmux-notices_test.py` passed on real local tmux 3.4/PTYS, including
  report scrolling/reopening, multiple clients, narrow terminals and delayed
  popup dismissal without terminal response leakage.
- Linux AMD64/ARM64 and Darwin AMD64/ARM64 launchers cross-compiled with
  `CGO_ENABLED=0`. The executable verifier accepted both static Linux binaries
  and both Darwin binaries using only system libraries. It rejected a dynamic
  system executable and an architecture mismatch. The local Linux AMD64 binary
  ran `version` and `--help`; other targets were inspected, not executed.

These results are development checkpoints, not release qualification. Re-run
the relevant gates against final artifacts, especially after adding the session
transfer dependencies and embedded Linux helpers. No Docker image was built or
published and no real conversation was transferred by this checkpoint.

## Session resolver checkpoint

`internal/session` now implements native history inspection and scoped writer
guards. Tests exercise committed SQLite WAL selection after revert, relocated
state, legacy schemas and copied metadata, compressed/archived files, nested
exact prefixes, malformed boundaries, conflicts, symlinks/special files,
cancellation and actual local flock ownership. The open publication and
runtime-compatibility questions are recorded in `session-transfer-design.md`.
The user-facing transfer commands are not implemented yet.

Verification for this checkpoint:

- The full Go suite and vet passed; session tests and optional native tests
  passed under the race detector. The vulnerability scan reported no findings.
- Native Codex 0.160.1 resumed synthetic legacy/paginated/fork/revert histories
  and sent their expected saved context to a loopback mock provider. Native
  picker selection and writer exclusion were checked. Archived resume required
  an explicit unarchive; the test then resumed the correct reverted rollout.
- Session test executables cross-built with cgo disabled for all four targets
  and passed executable linkage inspection. The Linux AMD64 static executable
  ran its tests. Other target binaries were inspected, not executed; these
  development test binaries are not qualified release artifacts.
- Dependency notices are bundled for archives/packages, and the license and
  release-command checks pass. ShellCheck with the repository's `-x` setting
  passes for the changed shell scripts and release script they source.

These checks used only disposable synthetic conversations. No user session,
credential, workstation container or image was migrated or published.

## Session publication and helper checkpoint

The transfer core now streams a versioned tar envelope, validates it in private
destination staging, checks all existing rollout identities, and publishes with
exclusive hard links. Exact existing content is reused, including full ancestors
that satisfy a required prefix. Conflicts, truncation and invalid members cannot
overwrite existing history. Handled publication failures remove only the links
created by that attempt; a retry can use the verified staging data.

`cmd/hcorral-session` exposes the internal export/import protocol on stdio with
explicit Codex/SQLite homes and session IDs. It is not yet bundled or wired to
the user-facing launcher commands. It runs without a shell or Codex process and
handles signals while blocked on input. The original signal test exposed a
blocking inherited-pipe issue; pollable duplicates resolved it, and repeated
SIGINT/SIGTERM cleanup tests pass.

Verification at this checkpoint:

- The full Go suite and vet pass. The changed session, helper and command tests
  pass under the race detector, including the native 0.160.1-to-0.160.0 matrix.
- Native tests use the actual export/receive/publish pipeline for legacy,
  paginated, inherited, reverted and archived histories. Both fresh and already
  initialized homes are covered. After a completed native turn and clean app
  server shutdown, the written history is re-exported to the other Codex version
  and resumed. Saved user/assistant context survives; private parent continuation
  and unrelated conversations do not leak. Both version directions pass.
- The 0.160.0 Linux executable came from the official `rust-v0.160.0` release;
  its archive matched the release asset SHA-256
  `306865417d4ee7a927785852910a527f41e1e159add390ac5ae3accb67d44a13`.
- Session test executables cross-build with cgo disabled for Linux/macOS
  AMD64/ARM64 and pass the linkage gate. Static Linux AMD64 publication tests
  pass as UID/GID 1000:1000, 501:20 and 12345:23456. Other platforms remain
  inspected rather than natively executed.
- Linux AMD64/ARM64 helper builds pass static-linkage inspection. Their stripped
  executable sizes are approximately 7.5 MB and 7.3 MB before compression. These
  are development builds, not qualified release artifacts or bundled launchers.

Open work includes concurrent initial backfill, handling a longer required
prefix or a full parent after an earlier partial import, source/destination
alias detection, effective SQLite-home discovery, result/policy reporting,
helper bundling, and running/stopped/remote Docker integration. These are still
requirements to resolve, not waived acceptance gates.

## Transport and helper-packaging checkpoint

`internal/sessiontransport` now inspects the deployed numeric identity, image
ID, runtime home and storage rather than invoking Compose preparation. A
disposable helper serves both running and stopped workstations. Only selected
state and metadata mounts are attached; a separately mounted SQLite directory
is read-only and narrowed to its required subdirectory. Existing volume subpaths
are preserved. Image healthchecks, entrypoint, network access and automatic image
pulls are disabled. Cleanup verifies an operation-specific ownership token and
only stops/removes that helper, without removing its volumes.

The streaming controller uses the native Go implementation on the host and the
supplied Linux helper remotely. Source inspection completes before destination
initialization. Source writer locks are released before EOF permits destination
publication, so shared-storage aliases can reuse identical data without locking
themselves out. A validated completion result survives a subsequent remote cleanup
error; missing acknowledgements are not reported as proof of no destination
changes. Bounded configuration reads use the actual workstation's filesystem,
including its writable layer, without starting it or extracting files locally.

`build.sh` now prepares and validates both Linux helper payloads before building
launchers. Both compressed helpers total approximately 6.5 MB. The generated
payload files are ignored by Git. Native helper protocol and transfer tests run
against the embedded payloads as a build step. **The app does not yet reference
the transport package: this is prepared packaging, not evidence that the current
launcher binary already contains or exposes the feature.** Public command wiring
must make the package reachable and final launcher artifacts must be checked.

Local verification for this checkpoint:

- Go unit tests and vet pass; the changed transport/runtime/packager packages
  pass race checks. ShellCheck, Bash syntax and release-contract checks pass.
- Controlled Docker tests cover running/stopped storage selection, non-default
  IDs/groups, image-ID architecture selection, volume subpaths, unused image
  volumes, missing resources, replacement races, cancellation and cleanup.
- Both helper payloads pass static ELF architecture/linkage inspection. The
  embedded AMD64 executable runs its protocol and transfers synthetic histories
  in both directions, including source/destination symlink aliases. ARM64 payload
  inspection is not native execution.
- Transport test executables cross-build with cgo disabled for Linux/macOS
  AMD64/ARM64 and pass the linkage gate. These are development test binaries,
  not final launcher archives or packages.
- The static Linux AMD64 transport tests and its embedded native helper pass
  as UID/GID 1000:1000, 501:20 and 12345:23456, with supplementary group 44444.
  Tests verify destination ownership and 0600 file permissions. This is local
  subprocess evidence, not a Docker mount/entrypoint test.
- Stream tests cover early source failure, cancellation, lost acknowledgements,
  confirmed publication followed by remote failure, and bounded result handling.
  Config-read tests distinguish missing files from failed container inspection,
  reject extra/special/truncated archive members and enforce output bounds.

Docker is still unavailable locally. The Dockerized build step, running/stopped
helper containers, remote daemons, real mount permissions and independent remote
process cleanup remain unexecuted. Effective SQLite config discovery, public
commands/result reporting and the remaining consistency gates above are next;
this checkpoint does not complete phase 5 or the full proposal.

## Public session-command checkpoint

`hcorral session export/import <UUID> [host-codex-home]` now dispatches before
ordinary lifecycle/Compose/GUI preparation. It captures the host environment,
honors explicit path / `CODEX_HOME` / `~/.codex` precedence, verifies the selected
existing corral under a cancellable project lock, and refuses an explicit state
selection that disagrees with its deployed home. Human and JSON output preserve
a confirmed publication result even if later remote cleanup fails.

`internal/sessionconfig` resolves Unix system/user base TOML, local requirements,
legacy managed TOML and `CODEX_SQLITE_HOME`. Config paths are relative to their
file, while relative environment paths use the endpoint working directory. A
project config declaring `sqlite_home` requires an explicit endpoint path rather
than approximating Codex's trust rules. CLI-selected profile-v2 files, per-process
runtime overrides, cloud policy and macOS managed preferences are not discoverable
from these base files; database overrides from those sources require explicit
`--host-sqlite-home` / `--container-sqlite-home`. This boundary is documented in
help and README. Parse failures do not echo configuration contents.

Evidence at this checkpoint:

- Controlled public-command tests exercise both directions and running/stopped
  corrals, all host-path defaults, relocated authoritative SQLite selection,
  excluded credentials, ownership/state refusal and publication acknowledgement
  followed by cleanup failure. They run the actual transfer core through an
  injectable Docker runner, not a real daemon.
- Native Codex 0.160.0 and 0.160.1 created their databases at the locations chosen
  by discovery for default, relative environment and overriding relative user
  config cases. These tests use disposable homes and no account credentials.
- Both Linux and both macOS launchers cross-build with cgo disabled and pass
  linkage inspection. A new final-executable gate verifies the exact AMD64 and
  ARM64 helper payload bytes are present in every launcher; it rejects a changed
  expected payload. Native Linux AMD64 runs the public session help command.
- Stripped development launcher sizes are approximately 18.1 MB Linux AMD64,
  17.6 MB Linux ARM64, 18.2 MB macOS AMD64 and 17.7 MB macOS ARM64. These are
  complete development executables, not qualified release archives/packages.

Public wiring and prepared packaging no longer remain missing. Remaining work
includes concurrent initial indexing, compatible prefix extension/promotion,
uncatchable-interruption recovery, broader writer/runtime qualification, actual
Docker transfers and cancellation, real native platform/package/image acceptance,
and the demonstrated myCodex transition. No user conversation or workstation was
migrated, no image/release was published, and the full goal remains incomplete.

## Compatible inherited-prefix extension checkpoint

A second fork with a longer inherited prefix now succeeds after an earlier
shorter import. Every existing managed representation is checked before writes;
only a complete byte-for-byte extension may atomically replace a prerequisite.
Regular conversation files are never extended or replaced. Repeated shorter
imports do not truncate history, and handled later failures retain a valid
extension with explicit retry information. Human/JSON results report extensions.

Core tests cover plain, compressed and duplicate representations, preserved open
readers, unchanged dependent files, divergence, later main conflicts, a replaced
prerequisite and publication failure followed by retry. Native Codex 0.160.0 and
0.160.1 both resume the old and new children after extension, with plain and
compressed prerequisites. Requests to the loopback fixture provider contain
exactly each child's inherited range and exclude the parent's private tail.

Final local checks for this checkpoint:

- The full Go suite and vet pass; session, transport and app race checks pass.
- The existing native 0.160.0/0.160.1 transfer/resume/completed-turn round-trip
  matrix still passes in both directions, along with configuration discovery
  and the new extension cases.
- Static Linux AMD64 extension tests pass as 1000:1000, 501:20 and 12345:23456
  with supplementary group 44444, including preservation of an existing file's
  non-primary group and Unix mode bits.
- Both Linux helpers were rebuilt. The actual embedded AMD64 helper imports
  shorter/longer forks, reports the extension and re-exports the longer child;
  ARM64 is inspected, not executed locally. Compressed payloads total 6,730,498
  bytes.
- All four final development launchers cross-build, pass linkage inspection
  and contain both exact helper payloads. Their sizes are 18,596,024 bytes
  (Linux AMD64), 18,022,584 (Linux ARM64), 18,685,280 (macOS AMD64) and
  18,190,770 (macOS ARM64). Native Linux AMD64 runs the public session help.
  These remain development artifacts, not qualified release archives/packages.

This resolves compatible growth, not complete-parent promotion or simultaneous
initial indexing. Those cases, uncatchable-interruption recovery, actual Docker
transport and the remaining image/platform/transition matrix stay open.

## Parent promotion and native indexing checkpoint: 2026-10-07

An explicitly imported complete parent can now replace its classification as a
partial prerequisite without changing existing children's inherited boundaries.
Managed plain/compressed representations grow only after their full existing
bytes have been validated. The complete parent is installed at its ordinary
native location; a subsequent native fork can inherit its full continuation.

When destination indexing selects a prerequisite, the importer repairs only the
requested thread's path/archive fields under an existing, qualified SQLite
schema and a write reservation. It waits for overlapping initial indexing while
retaining native writer guards. Names and unrelated metadata are preserved;
unknown layouts/triggers are refused. Separately mounted destination metadata
retains its deployed access for import; export remains read-only. No read-only
mount is made writable.

Fully validated live files now remain after failed publication, because native
indexing may already have recorded their paths. Retrying verifies/reuses those
files and completes selection repair. Private unpublished staging is cleaned on
handled failures. This supersedes the earlier reverse-unlink cleanup policy.
Uncatchable-interruption cleanup of abandoned private staging remains open.

Local evidence for this checkpoint:

- The full Go suite and vet pass. Session, transport and app race checks pass,
  including the native 0.160.1-to-0.160.0 completed-turn round-trip matrix.
- Native 0.160.0 and 0.160.1 both pass complete-parent promotion with active and
  archived parents, plain/compressed prerequisites, original-child resume and
  a new native fork after promotion. Native indexing between prerequisite and
  main publication, and an interrupted attempt followed by retry, both select
  and resume the complete requested revert without its private parent tail.
  The existing transfer/resume matrix still passes in both version directions.
- Controlled tests cover running-index waits/cancellation with writer guards
  retained, schema/checksum/trigger refusal, divergent/shorter histories,
  preserved names/unrelated rows, interrupted metadata commit and repeat imports.
  Docker mount tests verify import/export access and deployed read-only settings.
- Static Linux AMD64 promotion, selection-repair and ownership tests pass as
  1000:1000, 501:20 and 12345:23456, with supplementary group 44444. No host account
  setup or user-home changes are involved.
- Both Linux helpers were rebuilt and pass static-linkage validation. The
  embedded AMD64 helper extends prerequisites, promotes/re-exports a complete
  parent and transfers in both directions. ARM64 is inspected, not executed.
  The compressed payloads total 6,786,478 bytes.
- All four development launchers and session test executables cross-build with
  cgo disabled and pass linkage checks. Each launcher contains both exact helper
  payloads. Launcher sizes are 18,698,424 bytes (Linux AMD64), 18,153,656 (Linux
  ARM64), 18,788,240 (macOS AMD64) and 18,290,402 (macOS ARM64). Native Linux AMD64
  runs the public help command. These are not qualified release packages.

The proposal remains incomplete. Abandoned staging recovery, broader writer and
metadata-operation qualification, actual Docker/remote transfers and cancellation,
native platform/image/package acceptance, mixed launcher/image compatibility and
the demonstrated myCodex transition remain required. No image or release was
published and no real user conversation or workstation was migrated.

## Abandoned staging recovery checkpoint: 2026-10-07

Transfers now use versioned private staging with a kernel-held lease and a short
creation/recovery/close coordinator. A subsequent import can discard a recognized
abandoned staging directory after process death, without using its age or a PID
as evidence. Active transfers, other owners, unfamiliar layouts and published
history are preserved. Creation and cleanup sync ordering keeps an empty
lease-free directory recoverable across interruption. The full protocol and
filesystem assumptions are recorded in `session-transfer-design.md`.

Local verification:

- Real subprocesses are killed with SIGKILL during directory creation, after
  complete staging, after prerequisite publication and after full publication.
  Concurrent receives preserve live staging even with an old mtime; retry after
  confirmed death removes the orphan and preserves published inodes and bytes.
- Tests preserve old/future staging namespaces, unknown files, symlinks, FIFOs,
  nested directories, missing/changed/hard-linked leases, foreign owners and
  inaccessible directories. Multi-batch enumeration and cancellation of a wait
  for the creation coordinator are exercised.
- The full Go suite and vet pass. Changed session/helper/transport/app packages
  pass race checks. The native Codex 0.160.1-to-0.160.0 transfer/resume/re-export
  matrix, parent promotion/new fork, index overlap and configuration tests pass.
- The static Linux AMD64 recovery suite passes as UID/GID 1000:1000, 501:20 and
  12345:23456, each with supplementary group 44444. A root-only fixture verifies
  preservation of another UID's private staging directory.
- Rebuilt embedded Linux AMD64 helpers pass a killed-import/retry test and the
  existing transfer/extension/promotion matrix. ARM64 is statically inspected,
  not executed locally. Compressed helper payloads total 6,799,367 bytes.
- Session test executables and launchers cross-build for Linux/macOS AMD64/ARM64
  and pass linkage checks. Each final development launcher contains both exact
  helpers. Launcher sizes are 18,723,000 bytes, 18,219,192 bytes, 18,816,944 bytes
  and 18,323,474 bytes respectively. These are development builds, not release
  packages or native macOS/ARM64 runtime qualification.

Power failure was not simulated; sync ordering and actual process-crash recovery
are distinct evidence. Broader writer/filesystem and metadata-operation
qualification, actual Docker/remote execution and cancellation, native image and
platform/package checks, mixed launcher/image compatibility and the demonstrated
myCodex transition remain required. No publication or user-state migration was
performed at this checkpoint.

## Docker acceptance wiring checkpoint: 2026-10-07

The integration suite now invokes the final launcher and its embedded helper
against real Docker volumes through a standalone Go test executable. Both Linux
architectures are wired into CI with current and pinned Compose. The release
Colima suite also receives its native acceptance executable. Test binaries travel
in the CI artifact, but are excluded from user archives/packages.

Cases cover running/stopped workstations, three numeric UID/GID pairs, explicit,
environment and default host homes, exact history bytes, private file ownership,
repeat/conflicting transfers, excluded state, read-only storage, a separate
container holding a writer lock, and cancellation/retry with an actual helper
blocked on destination coordination. Workstation IDs, lifecycle timestamps,
mounts, existing credentials/configuration/history and volume inventory are
checked for preservation. No fixture Codex or image-installed helper is used.

The full local Go suite and vet, ShellCheck and release-contract checks pass.
Acceptance executables cross-build for all four targets and pass linkage
inspection; workflow lint passes.
Ordinary Go tests skip these explicit Docker cases without their fixture/image
environment. The Docker assertions themselves still require execution. This suite is transport
evidence, not native Codex resume or production-image shell acceptance. Remote
disconnects, unusual mount semantics and the broader runtime/platform/transition
matrix remain separate gates.
