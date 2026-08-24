# ADR 0005: Build and release model

Status: accepted

The launcher and the Codex, Claude, and Pi image streams are independently
versioned. Pinned Dockerized tools produce launcher artifacts. Preparation is
non-publishing and records hashes; publication consumes those exact bytes
through protected GitHub environments. Linux packages, GitHub assets,
Homebrew, and the three multi-architecture GHCR streams are qualified and
published by separate workflows.
