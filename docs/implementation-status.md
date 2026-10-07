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
| Shared shell defaults and preserved existing homes | Native image and shared-home canaries passed | Codex/Claude/Pi AMD64/ARM64 entrypoint/home tests and three UID/GID pairs pass; `43df6a4` also passes concurrent old/new image shared homes |
| GUI badge and retained/reopenable tmux reports | Native Linux image matrix passed | Launcher-embedded helper, deployed identity/home/session, `notices`; actual four-combination PTY/state checks pass at `43df6a4`, plus local scrolling/handshake tests |
| Automatic GUI with headless, SSH and remote fallback | Native Linux protocol matrix passed | Resolver tests and hosted Xvfb/Weston/XWayland production-UID tests pass at `43df6a4`; physical desktop release coverage remains separate |
| Inactive `latest` refresh and guarded stopped replacement | Native Linux Docker checks passed | CI at `c28f31d` passes lifecycle/refresh/runtime integration on AMD64/ARM64 with current and 2.24.6 Compose; remaining platform qualification still applies |
| Actual deployed image identity | Implemented | Docker `Image` ID retained separately from configured reference; mutable-alias and bundled/installed-version tests pass |
| Numeric process identity and groups | Native Linux and macOS launcher checks passed | `9a71bca` compares packaged UID/GID/groups on both Linux architectures; `4cb9129` passes the corrected POSIX process-group comparison on both Macs; all six Linux images pass group-based file access and denial; static unknown-account cases also pass |
| Bounded probes and readiness | Native Linux production-image checks passed | `f10c95c` passes blocked login/executable probes, runtime UID, container-side monitor/child reaping, bundled-version fallback and recovery on AMD64/ARM64 |
| Read-only discovery | Implemented | Separate GUI Discover/Prepare paths; credential-inode preservation test; Compose cache effects documented |
| Static binaries and packages on four targets | Package execution passed; complete runtime qualification remains | `df30f26` builds all four targets with both Linux helpers, passes both Linux package jobs and both macOS Homebrew audit/version/install/exact-binary checks; remaining macOS endpoint checks and exact final artifacts still need qualification |
| Manual image build workflow | Preserved; publication not performed | Separate native architecture workflow documented; builder Python dependency distinguished from launcher prerequisites; publication contract tests pass |
| Mixed launcher/image versions | Native Linux matrix passed | At `f10c95c`, both architectures pass all four actual v0.1.0/current launcher and baseline/current recipe combinations, plus three-UID shared homes; transition evidence is recorded separately |
| State-preserving myCodex transition | Native Linux named-volume matrix passed | `f10c95c` passes original launcher/recipe, copied/reused homes, preserved identity/metadata, native picker/resume, stopped import and return to myCodex on AMD64/ARM64 |
| Session format discovery and dependencies | Core implemented; broader qualification pending | Rooted inspection; legacy/paginated, archive/Zstandard, authoritative revert selection and exact inherited prefixes; native 0.160.0/0.160.1 fixtures and completed-turn re-export pass |
| Session consistency and conflicts | Core, extension, promotion, index repair and staging recovery implemented; qualification remains | Read-only source selection; writer guards; compatible prefix growth/promotion; native initial-index overlap; actual SIGKILL recovery preserves active staging and published history; broader writer/runtime/filesystem qualification remains |
| Session endpoints and helper distribution | Implemented; broader qualification pending | Public commands, base file/env SQLite discovery with explicit overrides, inspected storage/identity, streaming and bundled helpers; all four development launchers contain both exact payloads; native endpoint/package matrix remains |
| Session transfer lifecycle | Native Linux passed; macOS exposed unsafe shared locking | `4cb9129` passes both Mac endpoint matrices except host/guest writer exclusion; unsupported shared-storage refusal and its nested-lock regression now require real qualification; named-volume public Docker/native composition remains |
| Actual Codex resume acceptance | Native Linux public Docker matrix passed; broader qualification remains | `9a71bca` passes public export, native resume/completed turn, stopped import/re-export and peer resume in both 0.160.0/0.160.1 directions, fresh/initialized homes, both Linux architectures and Compose variants; macOS composition remains |

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

## First Docker CI results and transport corrections: 2026-10-07

[CI run 37553065955](https://github.com/infrasecture/hcorral/actions/runs/37553065955)
tested pushed commit `fb16f6b88323e9f6a85c7b53992df27f64406018`. It is a failed
run, not a completed qualification:

- All six Codex/Claude/Pi image jobs passed on native AMD64/ARM64. The build
  script invokes the entrypoint and persisted-home canaries, including the three
  UID/GID pairs and fresh/old-marker/custom/empty/symlink/login-file cases.
- Source checks, the four-target artifact build, Linux package jobs, and native
  macOS AMD64/ARM64 launcher version/help checks passed. ARM64 Arch coverage
  extracts and executes the package; it is not a native package-manager install.
- Both ARM64 Docker jobs passed lifecycle, refresh, runtime contracts, session
  import/export at all numeric identities, repeated/conflicting copies,
  read-only refusal and external-writer refusal. Cancellation exposed a real
  error-classification bug: a killed Docker client lost `context.Canceled`,
  producing exit 1 instead of 130. The AMD64 integration jobs were cancelled by
  matrix fail-fast; they do not count as passes.

The command runner now retains the cancellation cause alongside the child exit
error. Session exit classification uses the caller's context so internal peer
cancellation after a conflict does not disguise that conflict as a user abort.
Real local child-process tests cover cancellation/output retention; success and
ordinary failure remain distinct. CI matrix fail-fast is disabled for these
integration jobs to retain independent architecture evidence.

Storage inspection now preserves bind propagation and recursive settings,
rejects unqualified consistency/relabel modes, and refuses unsafe narrowing of a
nonrecursive metadata bind. Tests cover inspection/argument mapping, changed
settings after helper copy, and actual Docker bind/volume-subpath transfers.
The Docker cases and corrected cancellation must pass a subsequent run before
being marked qualified. No images, releases or real user state were published
or migrated by this development workflow.

## Native platform qualification wiring: 2026-10-07

The build now emits a native session-core test executable for every launcher
target. CI and release qualification invoke it against pinned, checksum-verified
official Codex 0.160.0/0.160.1 assets in both directions. It runs the existing
core/native resume, completed-turn round-trip, promotion, indexing, configuration
and process-crash tests with synthetic homes and a loopback-only provider.
The new runner passed locally on Linux AMD64 against both downloaded releases;
the other native platforms still require their CI results.

The ordinary CI macOS Intel job now also runs the existing Colima lifecycle
suite and the packaged session endpoint tests. This uses the previously defined
release-platform setup and does not publish anything. The macOS ARM64 hosted
runner still establishes native core/launcher behavior only, not a local Docker
acceptance result. Checksums and support boundaries are documented under
`tests/qualification/`; these test executables are excluded from user packages.

[CI run 37553861532](https://github.com/infrasecture/hcorral/actions/runs/37553861532)
tested `401c391`. Both architecture/Compose variants completed instead of
cancelling peers. Transfers, ordinary/nonrecursive binds, volume subpaths,
cancellation with remote cleanup/retry and writer refusal passed. The remaining
failure was setup of the explicit-private bind fixture: Docker prohibits that
propagation beneath its own data root. The fixture now allocates an ordinary
unique daemon-side temporary directory, preserving the private-bind case and
the remote-client path boundary. This correction and the new native platform
runner require the next CI result; neither failed run is presented as green.

## Linux endpoint results and mixed-version qualification: 2026-10-07

[CI run 37554555815](https://github.com/infrasecture/hcorral/actions/runs/37554555815)
tested `c28f31d`. Source, all six production-image jobs, four-target builds,
Linux packages and all four architecture/Compose integration jobs passed.
The corrected private-bind fixture now passes with the other storage cases.
Both Linux architectures also ran the native Codex 0.160.0/0.160.1 core suite
in both directions. This joins existing native resume and Docker transport
results; it still does not prove public Docker transfer followed by native resume.

Both macOS jobs failed before the native suite: the runner's non-GNU sha256sum
rejects GNU long options. The checksum runner now uses the supported short `-c`
option, retaining pinned digest verification. Release artifact verification on
macOS uses the same portable option. Native macOS results and Intel Colima
integration require another run; the failed jobs are not counted as passes.

Actual old/new launcher/image qualification is now wired into CI and release
Linux gates on both architectures. The test verifies a published v0.1.0 launcher,
builds a pinned historical image recipe and the current recipe without pushing,
and checks all four combinations, retained tmux reports and unchanged state.
It also tests startup files shared with an older running image at three UID/GID
pairs. Shell/workflow checks and release command-contract checks pass locally;
the mixed-version runtime suite requires real CI execution.

## Original myCodex transition and native endpoint qualification: 2026-10-07

[CI run 37555406831](https://github.com/infrasecture/hcorral/actions/runs/37555406831)
tested `6d17691`. Existing source, image, package and Linux integration/native
checks passed again. Both mixed-version jobs passed the concurrent old/new
image shared-home cases at all three UID/GID pairs. They then failed because
the fixture queried tmux immediately after detached creation, before entrypoint
readiness. The fixture now waits for actual startup readiness. The macOS
checksum compatibility command also rejected the short stdin-check invocation;
macOS now explicitly uses its `shasum -a 256 -c` implementation. These corrections
need a new run; neither failed matrix is qualified.

The transition guide now records inventory, quiescence, daemon-side home copy,
explicit volume selection, startup verification and recovery. The integration
fixture fetches the original pinned myCodex source and builds its real recipe,
uses its launcher for creation/removal, and exercises copy and explicit reuse
with hcorral. It checks synthetic credential/configuration/history preservation,
file ownership/modes/symlinks, legacy refusal, native picker/resume and return
through the original launcher. No legacy-container adoption code is introduced.

The copy scenario additionally composes public Docker session export with host
native resume, then public import into a stopped separate workstation followed
by container-native resume. This is a simple indexed-history endpoint fixture;
it does not replace the richer core/native history matrix. The test-only Python
app-server probe and exact synthetic seed pass locally with native Codex 0.160.0
and 0.160.1. ShellCheck, Bash syntax, workflow lint and release-contract checks
pass. Actual transition/endpoint execution remains pending CI. No real user
state, credentials, image publication or release is involved.

## Native GUI protocol qualification wiring: 2026-10-07

The repository currently has no registered self-hosted runners. CI and Linux
release qualification now provision real Xvfb, Weston and XWayland servers on
the native hosted runners instead of treating resolver unit tests as desktop
execution. The probe image derives from the newly built production Codex image,
adding diagnostic clients while retaining actual entrypoint and UID behavior.
Tests cover automatic selection (including Wayland preference), authenticated
X11/XWayland and Wayland protocol connections, narrow read-only mounts, deployed
tmux badges and unchanged attachment after display variables disappear. An
unusable explicit GUI request must leave the deployed container unchanged.

ShellCheck, Bash syntax, workflow lint and release-contract checks pass locally.
Native server/container execution remains pending CI. The servers use software
rendering on a disposable runner; this is not physical desktop, GPU or every
compositor's clipboard acceptance, and existing stable desktop gates remain.

## Platform results and diagnostic follow-up: 2026-10-07

At `63563cd`, CI run 37556175953 passed the native Codex suite on both macOS
architectures as well as both Linux architectures. The Intel Colima step was
still running when this checkpoint was recorded. Mixed-version shared homes
passed at all identities, and several actual launcher/image combinations passed
attachment/report/state checks. A later bare assertion failed in each mixed
job, before transition execution; the failing invariant was not printed, so the
root cause is not established by those logs.

Qualification scripts now report the failing assertion and canonicalize Docker
mount snapshots without dropping any fields. Older Docker inspection code
iterates a mount map without sorting; ordering alone cannot establish a changed
mount. The reopen probe now waits to see the retained report text in the PTY
before detaching, providing actual display evidence. These follow-ups require
another Docker run, not a claim that the unknown assertion failure is solved.

The macOS ARM64 Docker gate is also wired through Colima's x86_64 QEMU guest
mode: native ARM64 launcher/test binaries select the embedded AMD64 Linux helper.
This avoids relying on nested hardware virtualization and exercises differing
client/container architectures. It is a qualification attempt requiring actual
execution, not a native ARM64 guest result or a predeclared platform pass.
Local shell/workflow and release-contract checks pass; Go integration package
compilation passes with Docker cases skipped in this Docker-less local environment.

## Colima result and connection-fault qualification: 2026-10-07

[CI run 37556175953](https://github.com/infrasecture/hcorral/actions/runs/37556175953)
has completed with failure. Intel Colima ran the real lifecycle, refresh,
runtime, storage-mount, cancellation/retry and writer-refusal cases successfully.
The session export subtests stopped at their destination assertion: macOS's
`/var` temporary pathname resolves through `/private/var`, and the launcher
correctly reports its physical destination. The assertion now compares against
an independently resolved destination. The assertions after that failure still
need a successful run; this is not a qualified macOS endpoint result.

The Docker integration suite now also interrupts a real upgraded attach
connection while publication is blocked by a separate kernel-lock owner. It
requires bounded failure, helper removal, preservation of that unrelated owner,
and a successful retry. Another case drops the reply after the helper publishes:
independent Docker inspection must find the complete private history, the caller
must report an unconfirmed result, and retry must reuse the existing content.
A loopback TCP proxy forwards the real Engine API to its original Unix socket;
it changes only the selected connection, not daemon/helper responses. These
cases model connection resets and lost replies, not prolonged daemon outages
or SSH/TLS-specific failures.

A shared-storage case mounts an actual client-visible directory into the
workstation and transfers in both directions through a host symlink to it.
It requires unchanged history inode/bytes/permissions and no self-deadlock.
The disposable bind lives beneath the host home for Colima sharing; a marker
check establishes actual daemon visibility before history is seeded.

Local Go tests and vet pass. The proxy's upgrade, input half-close and reply-loss
plumbing passed repeated race tests. Acceptance executables cross-build with
cgo disabled and pass linkage verification on all four targets. These local
checks do not execute the new Docker cases; their real results, the corrected
macOS assertion, mixed-version diagnostics, GUI servers and transition fixture
remain pending the next CI run. No release or image was published.

## Public native-history composition gate: 2026-10-07

The native legacy/paginated/fork/revert/archive suite now has an explicit Docker
mode. It uses the packaged CLI to export independently seeded synthetic history
from a real container, then retains the existing native picker/resume and
loopback-provider context assertions. After the real Codex completes a turn,
public import writes its persisted history into another stopped container;
public export returns it to the peer native Codex for resume. Both 0.160.0 and
0.160.1 directions and fresh/already indexed host destinations are exercised.
Every public call must retain workstation identity/state/mounts and leave no
helper. The fixture copies only disposable synthetic setup data; it does not
change the public transfer's exclusion of databases or configuration.

The integration runner wires this composition gate on both Linux architectures,
both Compose variants, and the Colima platforms. Native fixture homes now use
the runtime `.codex` layout, and synthetic source databases are closed before
fixture seeding. The ordinary core tests keep using their in-process pipeline.
Local core/native tests pass after these fixture changes, including the reverse
version direction under the race detector. All four native test executables
cross-build with cgo disabled and pass linkage inspection; Go vet, ShellCheck,
workflow lint and release-contract checks pass. Actual Docker composition remains
pending execution; the simple transition/native probe is a separate gate.

At `43df6a4`, the ARM macOS CI job passed its native tests but could not start
the x86_64 Colima guest: Homebrew's base Lima installation omits that guest
agent. The CI and release setup now explicitly install
`lima-additional-guestagents` on ARM before Colima startup. That correction
requires another run; no ARM Docker result is claimed.

That same run passed Linux AMD64/ARM64 integration with current and pinned
Compose. The actual job logs explicitly show the connection-reset,
lost-completion/retry and shared-storage-alias cases passing on both native
architectures. This is real daemon/helper evidence for those new cases, not
merely a successful proxy unit test. Intel Colima and the mixed-version jobs
were still running at this checkpoint; the run is not a successful full matrix.

## Native lifecycle guards and ARM qualification results: 2026-10-07

The native writer-guard fixture now exercises archive, unarchive, resume and delete
against both legacy and paginated histories. While Go holds the selected
conversation's snapshot lock, native Codex must reject the operation as busy
without changing history bytes or authoritative selection. After release, the
same operation must succeed. All eight cases pass locally with 0.160.1 and with
0.160.0 under the race detector. Go tests/vet pass. The new cases join the
existing native platform runner, but other platforms require their next result;
older runtimes and other metadata/maintenance operations remain separate gates.

Both native Linux mixed-version jobs at `43df6a4` passed all four actual launcher/image
combinations, retained/reopened PTY reports, and the hosted Xvfb/Weston/XWayland
protocol suite as UID 1001. They then reached the myCodex transition for the first
time. Legacy refusal, copied-state preservation, shell customization and native
resume passed, as did public export followed by native host resume. Both receiver
fixtures failed before import because a fresh home does not yet contain `.codex`.
The fixture now creates its private configuration directory as the receiving
runtime UID before writing test configuration. Its stopped import, subsequent
resume, return to myCodex and reuse scenario still require execution. ShellCheck
and Bash syntax pass for that correction; the failed job is not full transition
qualification.

## Production version-probe cleanup gate: 2026-10-07

The mixed-version runner now also checks the version deadline against its real
new Codex image. The fixture verifies healthy native discovery, then separately
blocks a runtime user's login profile and user-prefix executable. Both ignore
TERM and spawn a child, requiring the existing container-side timeout to use
its kill deadline. Public `info` must return with the image's bundled version
and no invented installed version. All three recorded PIDs (timeout monitor,
shell and child) must be reaped, and the workstation's identity/image/mounts
must be unchanged. Normal discovery must recover after restoring fixture files.

ShellCheck and Bash syntax checks pass. Real Docker execution is pending; this
addition closes missing test coverage, not the acceptance gate itself. The
existing live Intel Colima job is retained while its result is still pending.

CI and release qualification now explicitly select Colima's VZ driver on Intel
macOS, matching the driver observed in the earlier actual integration log.
QEMU and additional Lima guest agents are installed only for the ARM host's
cross-architecture guest. Homebrew's current QEMU formula has no Intel macOS
bottle, so installing that unused emulator can introduce a source build.
This is a dependency correction, not a diagnosis of the still-running job:
its combined install/start/test step does not expose which operation is active.

## Native revert and selected-history transfer: 2026-10-07

A new native fixture completes two real turns, reverts before the second, and
exports/imports/resumes the native-created replacement. Its assertions cover
changed authoritative rollout with stable thread identity, exclusion of the
removed private continuation from the transfer stream and model context, and
busy snapshot refusal while the native writer is loaded before and after
revert. This extends the earlier hand-built revert fixtures with actual native
mutation. App-server revert requires a loaded thread, so an unloaded request's
`thread not found` result is not counted as a locking result.

The case passes locally on Codex 0.160.1 and in five race-enabled runs on
0.160.0. Full Go tests and vet pass. It joins the four-platform native runner;
these local results do not qualify the other platforms or the public Docker
composition. No image/release publication or real user-state migration occurred.

## Intel Colima endpoint result: 2026-10-07

The Intel macOS job [112590315045](https://github.com/infrasecture/hcorral/actions/runs/37558298578/job/112590315045)
at `43df6a4` completed successfully. Its real VZ/Colima run passed lifecycle,
refresh/configuration preservation, numeric transfer ownership at all three
UID/GID pairs, running/stopped endpoints and all host-path default variants,
bind/private/nonrecursive/subpath storage, shared-storage aliases, read-only
refusal, connection reset, lost-completion retry, cancellation/helper cleanup
and active-writer refusal. The prior macOS canonical-path assertion no longer
fails. This establishes those Intel endpoint cases; the newer native public
Docker composition and production-image probe tests were not in that revision.

The combined job step had installed QEMU 11.1.0 from a cached Sonoma bottle,
then used VZ and spent most of its time running tests. Its duration was not
evidence of a stalled source build. Removing the unused dependency remains an
appropriate correction but is not presented as fixing that completed run.

The run failed overall because the ARM Colima guest-agent and Linux transition
receiver fixtures failed as described above. After it became terminal, ordinary
nonpublishing CI was dispatched at `f10c95c` as
[run 37560678376](https://github.com/infrasecture/hcorral/actions/runs/37560678376),
covering those corrections, the production probe, native public Docker
composition and native lifecycle/revert checks. No earlier live job was canceled.

## Native metadata and maintenance qualification: 2026-10-07

Actual native Git metadata operations now verify both legacy rollout mutation
and paginated SQLite-only updates. Legacy mutation refuses a held transfer
guard without partially updating the database. Paginated metadata may change,
but the selected path and guarded rollout bytes must not. Retry after release
must succeed, and the resulting conversation must still transfer.

Compression and background legacy-to-paginated migration now run through actual
Codex workers in disposable homes. Unrelated eligible conversations must be
processed while the guarded one stays byte-identical; the selected conversation
must then process successfully after release. Compression requires its output
plus the released maintenance lock. Migration records a busy skip and clears it
on the next startup. The migrated/compressed native history must remain valid
and selectively transferable. This covers concrete metadata and maintenance
operations, not older nonparticipating writers or every filesystem.

These cases pass locally on 0.160.1 and in three race-enabled repetitions on
0.160.0. Session package tests and vet pass. They were added after the currently
running CI revision and still require their four-platform native results.

Meanwhile, `f10c95c` passed all four Linux integration jobs (both architectures,
current and 2.24.6 Compose). The completed AMD64/ARM64 logs confirm all ten
public Docker/native-history subcases in both version directions: legacy,
paginated, compressed inherited history, revert and archived revert, each with
fresh and already indexed destinations. Native follow-up and peer-resume context
assertions run after real public export/import/re-export. This closes the Linux
composition gap; macOS composition and the production transition/probe jobs
were still pending at this checkpoint.

## Production probe and complete transition results: 2026-10-07

Both mixed-version jobs at `f10c95c` completed successfully:
[AMD64](https://github.com/infrasecture/hcorral/actions/runs/37560678376/job/112597570554)
and [ARM64](https://github.com/infrasecture/hcorral/actions/runs/37560678376/job/112597570556).
Their completed logs confirm bounded blocked-login and blocked-executable
version checks, runtime UID, reaping of monitor/shell/child, correct bundled
fallback and restored healthy discovery, with workstation identity and mounts
unchanged. All four old/new launcher-image combinations, three shared-home UID
pairs, retained PTY reports and hosted display protocols pass again.

The full original-myCodex transition now passes both deliberate home copying
and explicit reuse, native picker/resume and return through the original
launcher. The copied-home case also passes public Docker export, host native
resume, import into a stopped receiver and native resume there. Source/destination
home metadata, volume labels and selected container identity checks remain in
the fixture. This qualifies those tested Linux layouts, not a real user migration
or arbitrary additional mounts/UID changes.

Both macOS jobs in that run remain active. The repository currently has zero
registered self-hosted runners, confirmed through the Actions API. A physical
Linux desktop host has been requested for the separate desktop gate; hosted
protocol success is not substituted for that evidence. The newer metadata and
maintenance tests at `41da45c` still need their native platform matrix.

## Packaged host identity qualification: 2026-10-07

The common Docker lifecycle fixture now compares the actual container's
launcher-supplied numeric UID/GID and complete supplementary-group set against
the invoking host process. It uses the packaged executable on Linux and macOS,
so unit checks alone cannot satisfy this assertion. The production-image probe
also verifies that its runtime process receives all host supplementary groups.
This closes a gap in test coverage between the process-identity implementation
and the independently tested image account mapping; execution is still pending.
The non-default image account case now reads and writes a root-owned file
through its supplementary group, then verifies access is denied when that group
permission is removed. This checks actual permissions as well as `id` output
for Codex, Claude and Pi. ShellCheck, Bash syntax and release-contract checks
pass; the actual image/platform matrix must run these new assertions.

## Nonpublishing Homebrew qualification: 2026-10-07

The package audit found that ordinary CI exercised macOS archives but left
Homebrew audit/installation solely in the publishing release workflow. Formula
generation now belongs to the shared release build: it hashes the two archives
from that build and emits the same formula used for publication. A partial
target build cannot generate a formula by borrowing a stale archive. Publication
still consumes its recorded prepared artifacts and retains its existing gates.

Both CI and release qualification now call one native macOS Homebrew fixture.
It audits the actual release formula, installs the selected unpublished archive
through a local tap, compares the installed executable with the build artifact,
and runs the formula test. It refuses an existing hcorral installation or fixture
tap and cleans up its own installation/tap. This requires no release publication.

ShellCheck, Bash syntax, workflow lint and the release-contract checks pass.
The contract fixture renders from actual test inputs, verifies both URL/digest
pairs, and rejects invalid versions or a missing archive without changing prior
output. Actual Homebrew execution remains pending the next native platform run;
the live `f10c95c` CI run predates this addition and has not been canceled.

## ARM Colima cleanup result and correction: 2026-10-07

The ARM macOS job [112597570438](https://github.com/infrasecture/hcorral/actions/runs/37560678376/job/112597570438)
at `f10c95c` successfully started its emulated x86_64 Colima guest. It passed
lifecycle, refresh, runtime safety, all three UID/GID endpoint pairs and path
defaults, mount variants, transport faults/retry, cancellation and writer refusal.
The shared client/daemon storage-alias import returned a confirmed identical
conversation, then failed cleanup with `directory not empty`. The job failed;
the following public Docker/native-resume composition did not run. Intel was
still active when this result was inspected.

The cleanup code unlinked its `.lease` while retaining its open descriptor.
The ARM guest logs show SSHFS setup, and libfuse documents that deleting an open
file can retain a hidden file until close. That is consistent with the observed
empty-directory failure; the job did not capture the hidden directory entry.
Normal close and orphan recovery now close the stage lease while retaining the
coordinator, before unlinking. Other cooperating cleanup operations remain
excluded. Unknown staging and published history retain their existing safeguards.

Full Go tests and vet pass, as do three race-enabled repetitions of the focused
recovery/publication tests, including actual SIGKILL boundaries. A local syscall
trace confirms lease descriptors close before unlink in both recovery and normal
close. The real ARM Colima regression still requires the next run; this is not
yet a successful macOS endpoint or general shared-filesystem qualification.

The audit also identified that the previous storage-alias test verified only
identical-file reuse. A new actual endpoint case holds a native host writer lock
and requires container-side export refusal, then holds the lock in a container
and requires host-side source refusal during import. Each retries after release.
It qualifies cross-kernel lock behavior of the selected VM sharing driver,
independently of the cleanup correction. Local compilation/tests cannot establish
this property; real Linux and Colima execution is required before claiming it.
The endpoint package tests and vet pass, and its acceptance executable builds
with cgo disabled for all four launcher platforms. The actual new lock case has
not yet run against Docker or Colima.

## Updated platform matrix and Homebrew audit: 2026-10-07

The original `f10c95c` run is terminal. Its Intel job
[112597570541](https://github.com/infrasecture/hcorral/actions/runs/37560678376/job/112597570541)
passed all lifecycle and endpoint cases, including storage aliases, faults,
cancellation and writer refusal. It then failed resolving `github.com` while
downloading Codex again for public Docker/native-resume composition. That suite
did not execute; successful preceding endpoint tests are not a composition pass.

Nonpublishing [CI run 37563361356](https://github.com/infrasecture/hcorral/actions/runs/37563361356)
tests `9a71bca` on temporary branch `agent/qualify-go-successor-9a71bca`, preserving
the then-active older Intel job. All six native image jobs passed, covering the
new actual supplementary-group read/write and denied-access assertions for Codex,
Claude and Pi on AMD64/ARM64. The source and release-build jobs also passed.

The new ARM Homebrew gate reached real strict audit and rejected the formula's
explicit `version`, which is redundant with the release archive URL. Formula
generation now omits that field and retains versioned URLs, archive hashes and
the executable-version assertion. Local shell and release-contract checks pass;
native Homebrew audit/install still needs another run. No release was published.

## Reuse verified native qualification assets: 2026-10-07

To avoid the repeated-download DNS failure observed in the Intel job, the native
qualification runner now retains checksum-keyed public Codex archives within a
CI job. Both native and Docker composition invocations verify the pinned digest
before extracting fresh binaries. Downloaded bytes enter the cache only after
verification; failed downloads are cleaned up. Local runs use temporary storage
unless `HCORRAL_CODEX_TEST_CACHE` explicitly selects a reusable directory. Codex
homes and extracted executables remain private per invocation.

Both full native version directions pass locally after fetching the two pinned
archives, then pass again with `curl` replaced by a command that fails every
download. A corrupted cached artifact is refused before execution. ShellCheck,
Bash syntax and release-contract checks pass. The private test umask also exposed
a permission fixture that assumed `MkdirAll(0755)` bypasses umask; it now explicitly
sets its intended preexisting mode before testing that publication preserves it.

These changes address qualification reliability and fixture setup. The macOS
public Docker/native-resume composition, the staging cleanup correction and
cross-host shared-storage writer exclusion still require actual platform results.

## Native Linux follow-up and next candidate: 2026-10-07

Completed `9a71bca` Linux jobs
[AMD64](https://github.com/infrasecture/hcorral/actions/runs/37563361356/job/112606232617)
and [ARM64](https://github.com/infrasecture/hcorral/actions/runs/37563361356/job/112606232567)
confirm that the newer Git-metadata, compression and background-migration cases
pass against both pinned Codex versions. Both also pass the new actual shared
host/container writer exclusion in both directions, plus public Docker transfer
and native resume. The two pinned-Compose jobs and Linux package jobs passed.
The exact packaged process UID/GID/group comparison runs in the successful
lifecycle fixture. These results close those Linux gaps, not the macOS ones.

Both `9a71bca` macOS jobs stopped at the same redundant-version Homebrew audit.
They did not run the newer native metadata/maintenance, Colima cleanup or
cross-kernel writer tests. Nonpublishing
[CI run 37563991174](https://github.com/infrasecture/hcorral/actions/runs/37563991174)
now tests candidate `4cf6303` on `agent/go-successor`, containing the formula and
verified-asset-cache corrections. The older temporary qualification ref/run is
retained while its independent mixed-version jobs finish; remove that temporary
ref during final repository cleanup after its evidence has been recorded.

## Native macOS qualification corrections: 2026-10-07

[Run 37563991174](https://github.com/infrasecture/hcorral/actions/runs/37563991174)
at `4cf6303` completed with both macOS jobs failing. All Linux, mixed-version,
package, image, source and four-target build jobs passed. Both macOS jobs passed
the Homebrew audit/install/exact-binary gate. ARM also passed the native Codex
core suite; its later endpoint checks stopped at the process-group identity
assertion, before the Colima cleanup/shared-storage regression cases.

The ARM identity test compared Go's POSIX process groups with modern Python's
macOS account-access group list. The latter included group 400, absent from
the former. These are different interfaces, as documented by
[Python](https://docs.python.org/3/library/os.html#os.getgroups); the pinned Go
toolchain imports the plain `getgroups` symbol. The fixture now calls that POSIX
symbol directly through ctypes on macOS and still requires exact numeric group
equality. Launcher identity handling is unchanged; the new assertion requires
an actual macOS rerun.

Intel's second native version direction completed the failing subtest's resume
assertions, then failed temporary-directory cleanup while a background plugin
Git clone was still writing. Native history fixtures now disable unrelated
plugin startup and own a separate process group. Timeout/cleanup terminates
that group, including remaining children after a graceful app-server exit.
Graceful completed-turn shutdown remains required by the resume fixture. A
regression test uses a real background child retaining a pipe to verify cleanup
after both cancellation and normal parent exit.

Homebrew's temporary local archive URL also caused its inferred package version
to become `64`, despite installing the right executable. Qualification now
checks the version inferred from the real release URL, then preserves that
version explicitly only in the temporary local formula and checks it again.
The published formula still derives its version from the release URL. Cleanup
also consumes the complete formula listing instead of closing its pipe early.

The full Go suite and vet pass. Both native Codex 0.160.0/0.160.1 directions pass
locally with the revised process lifecycle; ten race-enabled repetitions of
the background-child regression pass. Native test executables cross-compile
for both macOS architectures. ShellCheck, Bash syntax, release-contract checks
and workflow lint pass. These are local checks, not macOS runtime acceptance;
the final Colima and public Docker/native-resume gates remain outstanding.

Nonpublishing [CI run 37565661591](https://github.com/infrasecture/hcorral/actions/runs/37565661591)
tests these corrections at `df30f26`. Its source, build and image jobs were
observed running after dispatch. The previous run is terminal and has not been
restarted or replaced while active. README/help now make the exit-or-unload
prerequisite and qualified writer versions explicit; that guidance does not
claim automatic detection of every process sharing a home or close the remaining
runtime/storage boundary requirement.

## Idempotent native process cleanup: 2026-10-07

At `df30f26`, both macOS Homebrew gates passed with the intended `0.0.0`
package version. Intel passed the full native Codex suite and entered its Colima
step. ARM's native conversation cases passed, but the newly added child-cleanup
regression failed after cancellation: its second process-group kill returned
`EPERM`. The ARM job failed and did not reach Colima; the Intel job was still
running when inspected.

Darwin's [process-group signaling implementation](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/kern/kern_sig.c)
filters zombie members and can return `EPERM` when no signalable member remains.
Cancellation and post-Wait cleanup now share one idempotent kill operation,
retaining its original result. This avoids signaling the already-terminated
group again without treating arbitrary permission errors as success. The
regression still requires the background child's inherited pipe to close.

One hundred race-enabled repetitions of the child-cleanup cases pass locally.
Session tests and vet pass, and both full native Codex version directions pass
with the revised cleanup. Native macOS execution of this correction remains
required; the successful Linux checks do not substitute for it.

## Native macOS cleanup correction verified: 2026-10-07

Nonpublishing [run 37566300381](https://github.com/infrasecture/hcorral/actions/runs/37566300381)
tests `4cb9129` on temporary branch `agent/qualify-go-successor-4cb9129`, preserving
the earlier Intel Colima job. The new
[ARM64 job](https://github.com/infrasecture/hcorral/actions/runs/37566300381/job/112615213388)
and [AMD64 job](https://github.com/infrasecture/hcorral/actions/runs/37566300381/job/112615213345)
both passed native archive execution, Homebrew audit/version/install/exact-binary
checks and the complete native Codex suite. This verifies the idempotent cleanup
correction on both macOS architectures. Both subsequently entered their Colima
steps; endpoint and public Docker/native-resume completion were not yet known.

The earlier `df30f26` run completed every Linux, image, package, source and build
job successfully. Its Intel Colima job remains active; its ARM job failed as
recorded above. Neither run is qualified overall from these partial results.
Remove the temporary qualification branch only after its run has finished and
the resulting evidence has been recorded.

## Required Compose rendering versus attach diagnostics: 2026-10-07

The `4cb9129` [AMD64 low-end Compose job](https://github.com/infrasecture/hcorral/actions/runs/37566300381/job/112615213337)
failed when the generic Compose-wrapper case exceeded the five-second capture
deadline during configuration rendering. This fixture deliberately replaces the
pinned command with a wrapper delegating to the runner's `docker compose`; the
failure is not evidence of a particular Compose 2.24.6 parsing defect. Other
Linux jobs passed. The log establishes deadline expiry, not why that rendering
invocation took longer than five seconds.

Required configuration rendering now has a 30-second per-call bound. Optional
pre-attach drift/image discovery instead owns a five-second overall context,
so a stalled diagnostic still permits attachment with an unknown-drift report.
Docker inspection and optional update deadlines are unchanged, and a shorter
caller deadline takes precedence over the longer render limit.

A real six-second shell renderer reproduces the old failure and passes with
the new limit, retaining both rendered image and hash results. A stalled real
child respects a shorter caller deadline. A separate controlled launcher test
uses a real stalled renderer and confirms timely attachment to the existing
container, an unknown-drift report and no reconciliation. Race-enabled compose,
app, runtime and update tests pass, along with the full Go suite and vet. These checks
do not replace rerunning the actual failed Docker/Compose qualification case.

## Colima shared locks and explicit storage refusal: 2026-10-07

Both `37565661591` and `37566300381` are now terminal failures. At `4cb9129`,
the [Intel](https://github.com/infrasecture/hcorral/actions/runs/37566300381/job/112615213345)
and [ARM](https://github.com/infrasecture/hcorral/actions/runs/37566300381/job/112615213388)
jobs passed packaged process UID/GID/groups, lifecycle, refresh/fallback,
runtime/mount/wrapper checks, disconnect/lost-reply recovery, all running/stopped
UID/path endpoint cases, daemon-local mount variants, cancellation and guest
writer refusal. Both also passed the alias transfer, verifying the earlier ARM
close-before-unlink cleanup correction on its actual sharing driver.

Both failed `TestDockerSessionSharedStorageWriterRefused`: export succeeded
while the native Mac held the required writer lock on the same shared files.
The older Intel `df30f26` job failed identically. Successful alias copying and
same-guest locking had not established host/guest exclusion. The subsequent
public Docker/native-resume composition did not run; these jobs are not passes.

The transfer core now rejects recognized VM-shared and network filesystem
classes rather than trusting a successful kernel-local flock. It checks opened
roots and descendants, so a nested writer-lock or history mount cannot inherit
the parent's storage decision. There is no bypass or automatic migration.
Native host homes and daemon-local container volumes remain the intended
transfer endpoints. `session-transfer-design.md` records exact platform rules
and the remaining limits: this does not establish absence of older writers or
qualify arbitrary layered/exported storage.

Real endpoint regressions now independently inspect the guest filesystem.
Native storage must still exclude writers and succeed after release; FUSE/9p
shares must return the explicit unsupported-storage error while busy and idle,
preserve existing history and refuse an import of previously absent history.
A new fixture puts only the lock directory on shared storage under a local home.
These are required real Docker checks, not skipped platform cases.

The full Go suite and vet pass. Session/helper/transport race checks and both
full native Codex 0.160.0/0.160.1 directions pass locally. Core test executables
cross-compile for both Macs; endpoint executables cross-compile for both Macs
and Linux ARM64. These local results do not qualify the new refusal on Colima.
The next CI run must execute it and the outstanding public composition, together
with the earlier required-render/optional-diagnostic timeout correction.

Nonpublishing [CI run 37569071931](https://github.com/infrasecture/hcorral/actions/runs/37569071931)
tests candidate `973bfb1` on `agent/go-successor`. Its source, four-target build
and six image jobs were verified running after dispatch. No older run was
cancelled: both previous runs were terminal before dispatch. Their evidence is
recorded above, and the completed temporary qualification branch
`agent/qualify-go-successor-4cb9129` has been removed. The repository still has
no registered self-hosted runner for the separate physical-desktop gate.
