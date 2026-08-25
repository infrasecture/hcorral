# Installation

## Linux

Linux is the primary target. GitHub releases provide packages for both
supported architectures:

| Distribution | AMD64 package architecture | ARM64 package architecture |
|---|---|---|
| Debian and Ubuntu | `amd64` | `arm64` |
| RPM-based | `x86_64` | `aarch64` |
| Arch Linux | `x86_64` | `aarch64` |

Static Linux archives for AMD64 and ARM64 are also available. Download packages
and `SHA256SUMS` from the same GitHub release, then verify the selected package
before installing it. The README contains concrete commands for the current
preview release.

## Runtime dependency

Install Docker and Docker Compose v2 separately. The lowest continuously tested
Compose release is v2.24.6; current Docker-provided Compose v2 is tested in the
same CI suite. Hcorral probes the required rendered-JSON and config-hash
capabilities instead of changing behavior based only on a version string.
Hcorral packages contain only the launcher and license/documentation metadata;
they do not install or alter Docker, images, configuration, or myCodex.

## macOS

macOS supports headless operation. Install the launcher from the Infrasecture
Homebrew tap:

```bash
brew install infrasecture/tap/hcorral
```

Raw AMD64 and ARM64 macOS archives are available as an alternative to Homebrew.
