# Test ownership and execution

Comprehensive behavior runs once on Linux AMD64. Linux ARM64 checks native
launcher/helper execution, numeric ownership, packages, images and two public
Codex journeys. Darwin launchers are cross-built and inspected; CI and release
do **not** execute macOS, Homebrew or Colima. `ci/darwin` retains its required
check name, but means artifact validation, not macOS runtime qualification.

## Commands

Build once with `./build.sh --release --cli-version v0.0.0 --packages`, then:

| Command | Responsibility |
| --- | --- |
| `./tests/run.sh core` | Go tests, race detector, fuzz, lint, PTY, provenance and release contracts |
| `./tests/run.sh linux` | Lifecycle, refresh, Compose/storage, Docker session transport, native Codex compatibility and public journeys |
| `./tests/run.sh architecture` | Lifecycle plus native helper/ownership and public journeys on the second architecture |
| `./tests/run.sh compose` | Actual Compose/storage contracts against the selected Compose executable |
| `./tests/run.sh codex` | Native Codex compatibility only |
| `./tests/run.sh journey` | Two public-command journeys in both pinned version directions |
| `./tests/run.sh compat` | Prepare old/new images once; qualify shared homes, launcher/image compatibility, version probes, GUI and migration |

Each command has a five-minute wall-clock deadline, including its fixture
setup and cleanup. Steps report durations; Go reports individual test and
transfer durations. These limits do not establish that a workflow's complete
critical path is below five minutes: artifact build, transfer, queueing and
dependent jobs must also be reported when assessing CI speed.

## Coverage map

| Behavior | Primary owner | Real boundary check |
| --- | --- | --- |
| CLI parsing, HOME/CODEX_HOME/explicit path precedence | `internal/config`, `internal/app/session_test.go` | One host path with spaces in `TestDockerSessionEndpoints` |
| History formats, archive selection, inherited prefixes, limits, privacy and conflicts | `internal/session` filesystem/SQLite tests | Native Codex format matrix; one archived inherited-history Docker journey |
| Repeat import/export, receiving permissions and preservation | Core/application tests | One real repeat/conflict case; three UID/GID cases, not their Cartesian product |
| Actual running/stopped workstation storage | `tests/integration/sessions_test.go` | Both states across the three identity cases |
| Named volumes, bind propagation, nonrecursive binds, subpaths, read-only storage | Target/argv tests in `internal/sessiontransport` | Explicit real mount cases |
| Identity/storage changes during config discovery and helper setup | Transport tests | Real container preservation assertions |
| Cancellation, connection loss and uncertain completion | Process/transport tests | Three distinct real Docker failure cases and retries |
| Active-source snapshots and destination writer exclusion | Real file/process/SQLite tests | Native Codex held-turn test; public held-turn journey; Docker writer-lock fixtures |
| Shared-storage aliasing and unsupported filesystems | Storage policy and process-lock tests | Linux client-visible bind and nested-lock fixtures; no current macOS VM qualification |
| Account mapping, supplementary groups, shared Bash defaults and persisted home | `tests/image` | Native images on both Linux architectures |
| Notices surviving tmux attachment, reopen and terminal replies | `tests/tmux-notices_test.py` with real PTYs | Lifecycle and old/new launcher/image attach |
| Lifecycle decisions, drift, image refresh and offline preservation | `internal/app` | Focused real lifecycle/registry scenarios |
| Compose rendering, overlays, sidecars and state ownership | `internal/compose`, `internal/app` | `runtime-contracts.sh`, also against minimum Compose on AMD64 |
| Migration without adopting or changing myCodex resources | Legacy guard/application tests | Actual pinned myCodex migration scenarios once on AMD64 |
| Embedded helper identity, linkage, distribution bytes | `build.sh`, packager tests | Native execution on both Linux architectures; installation of exact packages |
| Darwin artifacts | Cross-build linkage/helper checks plus `darwin-artifacts.sh` | Archive bytes, architecture, licenses and formula checksums; no native execution |

## Rules for adding tests

- Put combinations in the layer that owns the decision. A format, path spelling
  or Compose version does not justify repeating every Docker test.
- Native Codex tests and Docker journeys are separate named tests. There is no
  environment switch that changes the entire format matrix into a Docker suite.
- Prepare immutable images/registry once per suite; each case owns its mutable
  state. Share state only inside an explicitly ordered lifecycle scenario.
- The fixture owner removes its images and registry. Cases remove only their
  own containers, homes and volumes. Never prune a developer's daemon.
- Preserve real process, permission, mount and failure evidence. Fast mocks
  cover decisions, not claims about the host kernel or Docker.
- Configuration discovery has a command-count regression test. A normal
  six-file discovery uses ten Docker invocations; a successful transfer uses
  at most 24. Deeper workspace paths add reads, not repeated storage inspections.
- Do not retry failed assertions. Dependency downloads may have bounded retries.
- Run independent CI jobs on separate daemons. Integration cases still compare
  persistent volume inventories, so do not run these suites concurrently on a
  developer daemon that is creating or deleting unrelated resources.

Go module/build caches are shared between commands; test results are not cached
(`-count=1`). CI retains those caches between runs. Image build contexts exclude
Git metadata, distribution artifacts and compilation caches. Historical fixtures
use pinned published image digests and validate source revision, version and
architecture; they are downloaded, not rebuilt. Only the current recipe is
built. Cache reuse never substitutes a moving tag.

Image publication retains its native canaries on both architectures. Release
preparation builds one versioned artifact set; platform qualification consumes
those bytes instead of first testing a second unversioned build. Darwin release
records say `artifact-checked`, never `passed` native qualification.
