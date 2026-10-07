# Transition from myCodex

Hcorral does not take over, modify or delete myCodex containers, volumes or
images automatically. It creates its own containers. You can explicitly give a
new hcorral container your existing home volume, keeping your configuration,
logins, sessions and other home files.

## Quick start: keep your existing home

For the standard setup—home volume `codex_state`, the same host user and the same
home path—run this from your project's directory with both launchers installed:

```bash
docker volume inspect codex_state >/dev/null &&
myCodex down &&
hcorral --harness codex --state-volume codex_state
```

This checks that your home volume exists, removes this project's old container
without deleting its data, and starts hcorral using that same home. **Do not add
`-v` to `myCodex down`: that would delete the home volume.** Hcorral uses its own
Codex image; your existing myCodex image is left alone.

Keep using `hcorral --harness codex --state-volume codex_state` afterward. To make
the volume choice the default, add this to your host shell's startup file:

```bash
export HCORRAL_STATE_VOLUME_NAME=codex_state
```

Without that flag or setting, hcorral defaults to a separate `hcorral_state`
volume. Your old files would still exist, but would not be mounted in the new
container. The chosen volume is also shared by other hcorral projects using it.

The quick start reuses the original home, so Codex and your tools can update its
files. For an untouched original to return to, use the copy option below first.

## Private volumes and custom settings

Use `myCodex info` to check the home volume, container home path and user IDs.
Include your usual options, for example `myCodex --private-env info`.
If the volume is not `codex_state`, substitute its reported name in the
quick-start commands. Use the same myCodex options with `down` too.

If you customized the home path, set `HCORRAL_CONTAINER_HOME` to that same path
before starting hcorral. Run as the same host user to retain the numeric UID/GID
and supplementary groups. Recreate additional mounts with hcorral's `--volume`
or `-f` options, including any separately mounted Codex or SQLite directories.
Hcorral does not translate myCodex environment variables or Compose files
automatically; see [hcorral configuration](configuration.md).

A stopped myCodex container still belongs to myCodex. Hcorral refuses to operate
in its workspace until the original launcher removes that container with plain
`myCodex down`; stopping it is not enough.

## Optional: copy the home and keep the original

For a separate trial, copy the whole home into `hcorral-home`. First finish active
sessions and stop every container or host process writing to the old home. This
copies live databases and configuration as well as sessions, so all writers must
be stopped during the copy. To find containers sharing the default volume:

```bash
docker ps --filter volume=codex_state --format '{{.Names}}'
```

From the project's directory, run:

```bash
docker volume inspect codex_state >/dev/null &&
myCodex down &&
docker run --rm --network none \
  --mount type=volume,src=codex_state,dst=/source,readonly \
  --mount type=volume,src=hcorral-home,dst=/target \
  ubuntu:24.04 bash -euc 'test -z "$(ls -A /target)"; cp -a /source/. /target/' &&
hcorral --harness codex --state-volume hcorral-home
```

Docker creates the destination volume if needed; the copy refuses a nonempty
destination. It runs on the Docker daemon and preserves file ownership,
permissions and symlinks. If the copy fails, do not start hcorral on the incomplete
destination. Inspect it and choose a fresh destination for another attempt.

Keep using `--state-volume hcorral-home`, or set
`HCORRAL_STATE_VOLUME_NAME=hcorral-home` in your host shell. Substitute your actual
volume names when using private or custom storage. Bind-mounted homes and
separately mounted state need corresponding copies and mounts; the command above
copies one named volume only.

## What to check after switching

Confirm that your shell customizations work, Codex recognizes your login and
configuration, and your existing sessions appear and resume. Hcorral preserves
existing shell startup files and does not recursively change ownership of a
populated home. It adds missing shell startup files. Settings that reference
tools or paths available only in the old image may need adjustment.

If permissions are wrong, check the home path and UID/GID before changing files;
do not apply a recursive `chown` to the whole home as a default fix. Session
export/import copies individual sessions, not the configuration in a whole home.

## Going back

After a copied-home trial, run `hcorral --state-volume hcorral-home down`, then
start `myCodex` with its original volume and settings. The original home was not
modified by hcorral. Keep the old image version available too: a moving image tag
may now point to a newer Codex.

If you reused the original volume, its contents may have changed. Returning to
an older Codex version can require restoring a backup. Hcorral's normal teardown
retains explicitly selected home volumes; it does not relabel them.

The [transition fixture](../tests/qualification/mycodex-transition.sh) checks both
copying and reuse with synthetic homes, including startup files, ownership,
configuration, session resume and return to myCodex. See the
[execution ledger](implementation-status.md) for tested versions and results.
