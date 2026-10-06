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
| Session format discovery and dependencies | Pending | Legacy/paginated, forks, revert selection, archive and Zstandard |
| Session consistency and conflicts | Pending | Writer coordination, SQLite WAL, prefixes, no-overwrite publication |
| Session endpoints and helper distribution | Pending | Host CODEX_HOME, runtime identity, cross-architecture Linux helpers |
| Session transfer lifecycle | Pending | No workstation pull/start/recreate/attach; stopped helper path |
| Actual Codex resume acceptance | Pending | Isolated homes and controlled history checks for supported versions |

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
