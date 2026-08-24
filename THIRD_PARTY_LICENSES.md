# Third-party software and license inventory

This file describes direct third-party components selected by the hcorral
source tree. It is not a replacement for the license files shipped by those
components. `scripts/check-third-party.sh` keeps the fixed versions and license
classifications below aligned with the Dockerfile and image builder.

## Launcher

The Go launcher has no third-party Go modules: it uses the Go standard library
only. Release archives and Linux packages therefore contain the hcorral binary,
the AGPL license, README, and this inventory without vendored Go dependencies.

## Workstation image

| Direct component | Selected version | License or terms | Primary notice |
|---|---:|---|---|
| Ubuntu devcontainers base | Ubuntu 24.04, digest-pinned | Per-package licenses | <https://hub.docker.com/_/microsoft-devcontainers> |
| Node.js | 22.23.2 | MIT plus bundled-component notices | <https://github.com/nodejs/node/blob/v22.23.2/LICENSE> |
| OpenAI Codex CLI | selected per image release | Apache-2.0 | <https://github.com/openai/codex/blob/main/LICENSE> |
| Claude Code | 2.1.241 | Anthropic commercial or consumer terms; proprietary | <https://code.claude.com/docs/en/legal-and-compliance> |
| Gemini CLI | 0.56.0 | Apache-2.0 | <https://github.com/google-gemini/gemini-cli/blob/main/LICENSE> |
| OpenCode | 1.18.21 | MIT | <https://github.com/anomalyco/opencode/blob/dev/LICENSE> |
| GitHub CLI | Ubuntu repository-selected, base-build time | MIT | <https://github.com/cli/cli/blob/trunk/LICENSE> |
| Ubuntu packages listed in `image/Dockerfile` | Ubuntu 24.04 repository-selected, base-build time | Per-package licenses | Installed notices under `/usr/share/doc/<package>/copyright` |

Claude Code is installed unmodified from Anthropic's published npm package.
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
