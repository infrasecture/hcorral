# Provenance

## Behavioral and source baseline

- myCodex `origin/master`: `cf3ee24af2b077996ac176ffffd1e34e892df061`
  (inspected 2026-08-23).
- Vaka build/release model: `409ee53a8072282a2660f3e73ab1f204be17b4a9`.
- Discussion decision: <https://github.com/emsi/myCodex/discussions/15#discussioncomment-18127443>.

The repository owner confirmed authority to move, adapt, and license relevant
myCodex material under `AGPL-3.0-or-later`. Hcorral has fresh Git history; no
myCodex commits are grafted or replayed.

## Copied and adapted files

| Hcorral file | Source at the myCodex baseline | Adaptation |
|---|---|---|
| `image/Dockerfile` | `Dockerfile` | hcorral paths, labels, arguments, helper, schema |
| `image/entrypoint.sh` | `entrypoint.sh` | hcorral environment and extracted session helper |
| `scripts/build-workstation-image.sh` | `bin/build-codex-image.sh` | hcorral image/tag/control namespace |
| `scripts/lib/hcorral-image.sh` | `bin/lib/mycodex-image.sh` | hcorral labels, inputs, and registry identity |

Launcher behavior, fixtures, and tests are reimplemented in Go. Vaka source is
used only as evidence for pinned builders, reproducible artifacts, Linux
packaging, prepare/publish separation, and Homebrew release mechanics.
