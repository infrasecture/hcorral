# Configuration

Hcorral does not read repository-local configuration. On Linux it reads
`${XDG_CONFIG_HOME:-$HOME/.config}/hcorral/config.toml`; on macOS it reads
`$HOME/Library/Application Support/hcorral/config.toml`. It never creates or
modifies the file.

```toml
default_harness = "codex"

[harness.codex]
image = "ghcr.io/infrasecture/hcorral-codex:latest"

[harness.claude]
image = "registry.example/team/claude-workstation:approved"
```

Changing `default_harness` affects only later bare invocations. It does not
rename, stop, or recreate existing corrals. The compiled 1.x fallback is
`codex`.

## Precedence

Harness: `--harness`, `HCORRAL_HARNESS`, `default_harness`, `codex`.

Image: `--image`, `HCORRAL_IMAGE`, `[harness.<type>].image`, built-in `:latest`.
An unknown type matching `[a-z][a-z0-9_]{0,31}` requires an explicit image.
Full references, including registry ports and `@sha256:` digests, are passed
unchanged to Compose.

Other typed settings are:

| Environment | CLI | Default |
|---|---|---|
| `HCORRAL_WORKSPACE` | `--workspace` | caller directory |
| `HCORRAL_PROJECT_NAME` | `--project-name` | generated |
| `HCORRAL_STATE_VOLUME_NAME` | `--state-volume` | `hcorral_state` |
| `HCORRAL_PRIVATE_ENV` | `--private-env` | `false` |
| `HCORRAL_GUI` | `--gui`, `--no-gui` | headless creation |
| `HCORRAL_COMPOSE_FILES` | `-f` | `[]` |
| `HCORRAL_COMPOSE_COMMAND` | — | `["docker","compose"]` |
| `HCORRAL_CONTAINER_HOME` | — | caller's home path |
| `HCORRAL_WORKDIR` | — | workspace path |
| `HCORRAL_UPDATE_CHECK` | — | `true` |
| `HCORRAL_WAIT_TIMEOUT_SECONDS` | — | `30` |
| `HCORRAL_STARTUP_PROGRESS_INTERVAL_SECONDS` | — | `2` |
| `HCORRAL_BYOBU_SESSION` | — | `hcorral` |
| `HCORRAL_AUTO_ATTACH` | — | `false` |

`HCORRAL_COMPOSE_COMMAND` and `HCORRAL_COMPOSE_FILES` are JSON string arrays;
they are never shell-split. CLI overlays append after environment overlays.
Relative workspace, overlay, workdir, and bind-source paths resolve from the
original caller directory. `--` ends launcher option parsing and passes every
following argument as the selected command.

Unknown `HCORRAL_*` variables produce one warning and are ignored. Inherited
`COMPOSE_*` variables are removed from Compose children; Docker context and TLS
variables are preserved. `hcorral info --format=json` reports each resolved
source without exposing custom Compose command arguments.
