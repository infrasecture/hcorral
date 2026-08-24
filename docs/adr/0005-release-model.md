# ADR 0005: Build and release model

Status: accepted

Launcher and workstation-image versions are independent. Pinned Dockerized
tools produce artifacts. Preparation is non-publishing and records hashes;
publication consumes those exact bytes through protected GitHub environments.
Linux packages, GitHub assets, Homebrew, and multi-architecture GHCR images are
qualified and published by separate workflows.
