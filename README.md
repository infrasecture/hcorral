# Harness Corral

Harness Corral (`hcorral`) runs a persistent AI development workstation in a
Docker container. It is a native Go launcher for Linux and macOS hosts, with
optional narrowly scoped X11 or Wayland forwarding on Linux.

The workstation is intentionally configured for autonomous coding agents:
Codex uses `approval_policy = "never"` and `sandbox_mode = "danger-full-access"`.
Every mounted path is available to the agent. Docker provides convenient
process and filesystem separation, not a hardened security sandbox.

## Requirements

- Docker with Linux-container support;
- Docker Compose v2;
- Linux or macOS on amd64 or arm64.

macOS is permanently headless. X11 and Wayland forwarding are Linux-only.

## Quick start

```bash
cd /home/alice/git/payment-api
hcorral
```

For that exact path, the generated Compose project and container name is
`hcorral-payment_api-4324ea2`. The readable slug uses `_`, reserving the two
hyphens for the fixed `hcorral-<slug>-<7-hex>` structure. Docker ownership is
verified with the full 64-character workspace hash, never the short suffix.

Select a workspace without changing directory:

```bash
hcorral --workspace /home/alice/git/payment-api
HCORRAL_WORKSPACE=/home/alice/git/payment-api hcorral
```

Use private or explicit state:

```bash
hcorral --private-env
hcorral --state-volume team-codex-home
```

The default shared state volume is `hcorral_state`. Private state uses the same
name as the generated project/container because Docker keeps container and
volume names in separate namespaces. Explicit custom volumes are never removed
by `hcorral down -v`.

## Lifecycle

A bare invocation attaches to an existing running environment without pulling,
starting, reconciling, or recreating it. A stopped matching environment is
started without recreation. Configuration changes require explicit `up` or
`create`; `pull` only fetches an image.

```bash
HCORRAL_IMAGE_TAG=0.147.0-r2 hcorral pull
HCORRAL_IMAGE_TAG=0.147.0-r2 hcorral up -d
HCORRAL_IMAGE_TAG=0.147.0-r2 hcorral
```

Common commands:

```text
hcorral
hcorral attach
hcorral info [--format=human|json]
hcorral ps
hcorral start|stop|restart
hcorral exec <command...>
hcorral pull
hcorral up|create|down [arguments...]
```

Additional Compose overlays are ordered after the built-in definition:

```bash
HCORRAL_COMPOSE_FILES='["compose.audit.yaml"]' \
hcorral -f compose.local.yaml up -d
```

An argv-safe Compose-compatible wrapper can be selected without shell parsing:

```bash
HCORRAL_COMPOSE_COMMAND='["/usr/local/bin/policy-compose","compose"]' hcorral ps
```

## Linux GUI forwarding

```bash
hcorral --gui=x11
hcorral --gui=wayland
hcorral --no-gui up -d
```

X11 exposes only the selected display socket and a copied per-display cookie;
it never runs `xhost +`. Wayland exposes only the selected compositor socket,
not the complete runtime directory. GUI mode is container configuration and
changes only through explicit reconciliation.

## Existing myCodex environments

Hcorral is a separate project and provides no myCodex compatibility or
migration layer. If it verifies a myCodex container for the same physical
workspace, it exits without mutation. Use myCodex to attach, or run
`myCodex down` from the original workspace before using hcorral. Avoid
`myCodex down -v` unless deletion of its persisted home is intentional.

## Building

The build uses pinned Dockerized tooling; a local Go installation is not
required.

```bash
./build.sh
./build.sh --release --cli-version v0.1.0 --packages
```

Build a workstation image without publishing:

```bash
./scripts/build-workstation-image.sh --version 0.147.0 --revision 1
```

See [configuration](docs/configuration.md), [runtime model](docs/runtime-model.md),
[security](docs/security.md), and [maintainer documentation](docs/maintainers.md).

## License

Hcorral is licensed under the GNU Affero General Public License v3.0 or later.
See [LICENSE](LICENSE).
