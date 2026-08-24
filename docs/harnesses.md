# Harness images

Every image contains the same Ubuntu 24.04 workstation foundation, account and
group reconciliation, passwordless sudo, same-path workspace/home mounts, and
Byobu/tmux session contract. There is no `/workspace` compatibility path.

- Codex is an exact architecture-specific standalone GitHub release artifact,
  checksum verified. Startup writes container-local `/etc/codex/config.toml`
  with `approval_policy="never"`, `sandbox_mode="danger-full-access"`, and the
  exact workdir trusted. Persisted `~/.codex/config.toml` is untouched.
- Claude is installed with Anthropic's native installer at an exact version.
  `DISABLE_AUTOUPDATER=1` disables background replacement; `claude update` and
  `claude install <version>` remain available. Missing user settings default to
  bypass-permissions mode and existing settings/authentication are preserved.
- Pi is installed as exact `@earendil-works/pi-coding-agent` with
  `--ignore-scripts`. Missing `~/.pi/agent/settings.json` defaults project trust
  to `always`; existing settings and authentication are preserved.

Only the selected harness is installed in each image. A custom image receives
the same launcher environment and readiness/session expectations, but controls
its own tools, configuration, updater behavior, and security.

The runtime configures `~/.local/share/npm` as the user npm prefix and places
its `bin` directory, `~/.local/bin`, and `~/.cargo/bin` before image-owned
tools. Supported manual updates can therefore take precedence without changing
the image:

```console
# Codex
npm install --global @openai/codex@latest

# Claude Code
claude update
# or select an exact release
claude install <version>

# Pi
npm install --global --ignore-scripts @earendil-works/pi-coding-agent@latest
```

Such a change is not part of the image label; `info` probes the running
executable and reports it separately. Recreating the container removes
writable-layer changes but preserves these user-prefix installations in the
mounted home. Remove or replace the user-installed executable to return to the
selected image copy.
