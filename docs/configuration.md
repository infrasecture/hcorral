# Configuration

CLI values override `HCORRAL_*` environment values, which override fixed
defaults. Relative workspace, overlay, and extra-mount source paths resolve
against the original caller directory.

| Environment | CLI | Default | Effect |
|---|---|---|---|
| `HCORRAL_WORKSPACE` | `--workspace` | caller directory | Physical workspace identity and mount |
| `HCORRAL_PROJECT_NAME` | `--project-name` | generated | Literal project/container override; ownership still checked |
| `HCORRAL_IMAGE_NAME` | — | `ghcr.io/infrasecture/hcorral` | Reconciliation image repository |
| `HCORRAL_IMAGE_TAG` | — | `latest` | Reconciliation image tag |
| `HCORRAL_STATE_VOLUME_NAME` | `--state-volume` | `hcorral_state` | User-managed explicit state |
| `HCORRAL_PRIVATE_ENV` | `--private-env` | `false` | Per-workspace state volume |
| `HCORRAL_GUI` | `--gui`, `--no-gui` | unspecified/headless | Linux desktop bridge |
| `HCORRAL_COMPOSE_COMMAND` | — | `["docker","compose"]` | JSON argv array |
| `HCORRAL_COMPOSE_FILES` | `-f` | `[]` | JSON array followed by CLI overlays |
| `HCORRAL_CONTAINER_HOME` | — | physical host home | Same-path persisted home |
| `HCORRAL_WORKDIR` | — | physical workspace | Container working directory |
| `HCORRAL_UPDATE_CHECK` | — | `true` | Bounded informational check |
| `HCORRAL_WAIT_TIMEOUT_SECONDS` | — | `30` | Startup timeout |
| `HCORRAL_STARTUP_PROGRESS_INTERVAL_SECONDS` | — | `2` | Progress interval |
| `HCORRAL_BYOBU_SESSION` | — | `hcorral` | tmux session name |
| `HCORRAL_AUTO_ATTACH` | — | `false` | Entrypoint terminal behavior |

Boolean values are exactly `true` or `false`. State modes are mutually
exclusive. `HCORRAL_COMPOSE_COMMAND` must be a non-empty JSON string array;
elements are passed without shell evaluation. Arbitrary command prefixes are
redacted from diagnostics.
