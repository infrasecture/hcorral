# Third-party license inventory

Hcorral's Go launcher currently uses only the Go standard library. The
workstation image installs software distributed by Ubuntu, npm, Rust, and the
GitHub CLI package repository; their licenses remain those of their respective
packages. Release automation must regenerate and verify the dependency and
image-package inventory before a public release.

Copied and adapted implementation material is documented in
`docs/provenance.md` and is relicensed under `AGPL-3.0-or-later` with the
copyright owner's authorization.
