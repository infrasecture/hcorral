# Third-party software and license inventory

This file describes direct third-party components selected by the hcorral
source tree. It is not a replacement for the license files shipped by those
components. `scripts/check-third-party.sh` keeps the fixed versions and license
classifications below aligned with the Dockerfile and image builder.

## Launcher

The launcher and session-transfer core use these direct Go dependencies:

| Module | Version | Purpose | License |
|---|---|---|---|
| `github.com/pelletier/go-toml/v2` | `v2.2.4` | User configuration | MIT |
| `github.com/klauspost/compress` | `v1.20.1` | Streaming Zstandard decoding | BSD-3-Clause and bundled notices |
| `golang.org/x/sys` | `v0.47.0` | Rooted filesystem access and Codex writer locks | BSD-3-Clause |
| `modernc.org/sqlite` | `v1.59.0` | Read-only thread selection, including WAL | BSD-3-Clause and bundled notices |

SQLite v1.59.0 was selected to retain Go 1.25 compatibility. Its newer 1.60
releases require Go 1.26; a toolchain upgrade needs its own qualification.
All selected dependencies work with cgo disabled. No host SQLite or Zstandard
executable is invoked.

`THIRD_PARTY_GO_LICENSES.txt` preserves the full notices for linked modules,
including the SQLite translation's libc/memory components and the Zstandard
decoder's xxhash implementation. Release archives and Linux packages include
that file alongside the AGPL license, README and this inventory. Update the
notices when changing dependencies; an inventory entry alone does not replace
the upstream license text.

## Workstation image

| Direct component | Selected version | License or terms | Primary notice |
|---|---:|---|---|
| Ubuntu devcontainers base | Ubuntu 24.04, digest-pinned | Per-package licenses | <https://hub.docker.com/_/microsoft-devcontainers> |
| Node.js | 22.23.2 | MIT plus bundled-component notices | <https://github.com/nodejs/node/blob/v22.23.2/LICENSE> |
| OpenAI Codex CLI | selected per image release | Apache-2.0 | <https://github.com/openai/codex/blob/main/LICENSE> |
| Claude Code | selected per image release | Anthropic commercial or consumer terms; proprietary | <https://code.claude.com/docs/en/legal-and-compliance> |
| Pi coding agent | selected per image release | MIT | <https://github.com/earendil-works/pi/blob/main/LICENSE> |
| Ubuntu packages listed in `image/Dockerfile` | Ubuntu 24.04 repository-selected, base-build time | Per-package licenses | Installed notices under `/usr/share/doc/<package>/copyright` |

Claude Code is installed unmodified with Anthropic's supported native installer.
Anthropic states that preinstalling Claude Code in a product requires the
applicable Anthropic terms, preserving every built-in authentication method,
and requiring each end user to authenticate and pay under their own agreement.
Hcorral does not collect, intermediate, or provide Claude credentials or usage.
Image publishers and users remain responsible for satisfying the applicable
Anthropic terms.

The image keeps upstream package notices in place and installs this inventory
and hcorral's AGPL text under `/usr/share/doc/hcorral/`. Transitive npm and OS
packages retain the license metadata shipped by their package distributions.

Copied and adapted implementation material is documented in
`docs/provenance.md` and is relicensed under `AGPL-3.0-or-later` with the
copyright owner's authorization.
