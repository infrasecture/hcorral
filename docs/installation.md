# Installation

## Homebrew on macOS

```bash
brew install infrasecture/tap/hcorral
```

## Linux packages

GitHub releases provide amd64 and arm64 deb, rpm, and Arch Linux packages. Raw
static archives are provided for Linux and macOS on both architectures. Verify
downloads with the release's `SHA256SUMS` before installation.

## Runtime dependency

Install Docker and Docker Compose v2 separately. The lowest continuously tested
Compose release is v2.24.6; current Docker-provided Compose v2 is tested in the
same CI suite. Hcorral probes the required rendered-JSON and config-hash
capabilities instead of changing behavior based only on a version string.
Hcorral packages contain only the launcher and license/documentation metadata;
they do not install or alter Docker, images, configuration, or myCodex.
