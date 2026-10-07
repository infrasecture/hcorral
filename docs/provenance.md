# Provenance

## Behavioral and source baseline

- Current myCodex parity baseline: `ebc930ac00adea662789d6c2f43666ec1003eca0`
  (PRs #17/#18, refreshed 2026-10-06).
- Original myCodex source baseline: `cf3ee24af2b077996ac176ffffd1e34e892df061`
  (inspected 2026-08-23; retained here for the original adaptations).
- Vaka build/release model: `409ee53a8072282a2660f3e73ab1f204be17b4a9`.
- `infrasecture/homebrew-tap` implementation-start revision:
  `e39fd9dff3b0d91f277d7c02a598b348e2f10a9a`.
- Discussion decision: <https://github.com/emsi/myCodex/discussions/15#discussioncomment-18127443>.

The repository owner confirmed authority to move, adapt, and license relevant
myCodex material under `AGPL-3.0-or-later`. Hcorral has fresh Git history; no
myCodex commits are grafted or replayed.

## Copied and adapted files

| Hcorral file | Source at the original myCodex baseline | Adaptation |
|---|---|---|
| `image/Dockerfile` | `Dockerfile` | hcorral paths, labels, arguments, helper, schema |
| `image/entrypoint.sh` | `entrypoint.sh` | hcorral environment and extracted session helper |
| `image/session-init.sh` | session setup in `entrypoint.sh` | idempotent entrypoint and launcher recovery helper |
| `scripts/build-harness-image.sh` | `bin/build-codex-image.sh` | independent harness descriptors, image/tag/control namespace |
| `scripts/lib/hcorral-image.sh` | `bin/lib/mycodex-image.sh` | hcorral labels, inputs, and registry identity |

Launcher behavior, fixtures, and tests are reimplemented in Go. Vaka source is
used only as evidence for pinned builders, reviewable artifacts, Linux
packaging, prepare/publish separation, and Homebrew release mechanics.

## Go successor updates

The shell-default and notice changes adapt myCodex
`ebc930ac00adea662789d6c2f43666ec1003eca0` (PRs #17 and #18):

| Hcorral file | myCodex source | Adaptation |
|---|---|---|
| `image/entrypoint.sh` | shared shell initialization in `entrypoint.sh` | `/etc/hcorral/bashrc`, existing home initialization |
| `internal/app/assets/tmux-notices.sh` | `bin/lib/mycodex-tmux.sh` | launcher-embedded helper and hcorral session options |
| `tests/tmux-notices_test.py` | `tests/tmux-gui_test.py` | isolated PTY tests against the embedded helper |
| `tests/image/runtime-home.sh` | `tests/runtime-home_test.sh` | common per-harness image qualification |
| `tests/image/runtime-home-probe.sh` | `tests/runtime-home-probe.sh` | hcorral session names and harness-aware assertions |

The tmux helper and PTY tests also incorporate the terminal-handshake fix present
in the local myCodex worktree on 2026-10-06: schedule the popup asynchronously
after attachment so terminal replies do not leak into the shell. Those source
files were read without changing their worktree contents.
