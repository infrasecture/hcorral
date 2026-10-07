# Transition from myCodex

Hcorral deliberately has no compatibility or automatic migration layer. It
does not adopt myCodex containers, volumes, environment variables, Compose
files, images, or command names.

When a running or stopped myCodex container is verified for the same physical
workspace, hcorral exits with status 3 and performs no mutation. Use myCodex to
attach or run `myCodex down` in the original workspace first. Existing state
can be selected only by explicitly naming its Docker volume through the normal
`--state-volume` interface; hcorral never discovers or relabels it.

## Choose the scope

A transition creates a new hcorral container. It does not rename or convert the
myCodex container. Keep the same physical workspace path, container home path,
numeric UID/GID and required supplementary groups. Changing those at the same
time is a separate migration, especially when paths occur inside Codex metadata,
shell files or tool configuration.

For the first trial, copy the whole persisted home to a new, explicitly named
volume while every writer of the original home is stopped. This retains the
original home for recovery. Unlike selective `session export/import`, this
deliberately copies credentials, configuration, all conversations and other
home files. Treat the copy as private state. A snapshot of a live SQLite/WAL
directory is not a consistent backup merely because tar completed.

Direct reuse through `--state-volume` is also possible after removing the old
container, but hcorral and Codex may then change that same home. Preserve a
separate recoverable copy first. Hcorral preserves existing startup files and
does not recursively change their ownership. An unreadable home or startup
file needs an explicit, scoped repair, not a blanket recursive chown.

## Record and stop the original environment

Run the original launcher from the original workspace with its original flags,
image selection, overlays and environment. In the examples, replace all sample
values with the observed configuration before executing commands:

```bash
legacy_launcher=/absolute/path/to/myCodex/bin/myCodex
old_container=example-codex
"$legacy_launcher" info
"$legacy_launcher" exec id
docker inspect --format '{{.Id}} {{.Image}} {{.Config.Image}}' "$old_container"
docker inspect --format '{{range .Mounts}}{{println .Type .Name .Source .Destination .RW}}{{end}}' "$old_container"
```

Record the actual home mount and numeric identity, workspace and additional
mounts, configured image reference and immutable image ID. Do not infer a volume
name from the workspace basename. Shared, private, custom and overlaid mounts
can differ. Keep the old launcher, configuration and image available. Choose a
qualified hcorral Codex image; a similarly named myCodex image does not implement
hcorral's image environment contract.

Use an isolated disposable workspace to check the hcorral installation before
touching this project. The same-workspace legacy guard intentionally prevents
testing operational commands alongside the old project container.

For a named home volume, list all containers referencing it:

```bash
old_volume=observed-home-volume
docker volume inspect "$old_volume"
docker ps -a --filter "volume=$old_volume" --format '{{.ID}} {{.Names}} {{.Status}}'
"$legacy_launcher" stop
```

Finish active Codex turns and stop other writers of the same home through their
own launchers too. Stopping only this project does not stop another project
using shared state. For a bind-mounted home or separately mounted Codex/SQLite
directories, also account for host processes and every additional storage
location. The named-volume example below does not cover those layouts; use a
filesystem snapshot/copy procedure that preserves their complete, quiescent
state and mount arrangement.

## Make a separate home copy

Choose an unused volume name and a locally available qualified hcorral image
reference. This example runs tar on the Docker daemon, so neither home is
mistaken for a path on a remote Docker client's filesystem:

```bash
new_volume=example-hcorral-home
image=ghcr.io/infrasecture/hcorral-codex:VERSION-rREVISION
docker image inspect "$image"
if docker volume inspect "$new_volume" >/dev/null 2>&1; then
  printf 'Choose a new, unused destination volume name.\n' >&2
else
  docker volume create "$new_volume"
  docker run --rm --network none --entrypoint bash \
    --mount "type=volume,src=$old_volume,dst=/source,readonly" \
    --mount "type=volume,src=$new_volume,dst=/target" "$image" -c \
    'set -euo pipefail; test -z "$(find /target -mindepth 1 -maxdepth 1 -print -quit)"; tar --numeric-owner --acls --xattrs -C /source -cpf - . | tar --numeric-owner --acls --xattrs -C /target -xpf -'
fi
```

Run these steps deliberately: proceed only after volume creation and copying
both succeed, and verify the selected source/destination before startup. A
failed copy leaves an incomplete destination; it is not ready for reuse. The
tar operation preserves numeric ownership, modes, symlinks and supported
extended metadata instead of assigning the client user's ownership. It does
not translate home paths or repair inaccessible files. Keep the original volume
unchanged while validating the copy.

Remove the old project's container using its original configuration:

```bash
"$legacy_launcher" down
docker volume inspect "$old_volume"
```

Use plain `down`, without a volume-removal option. A stopped legacy container
still triggers hcorral's guard. Do not bypass the guard by editing labels or
renaming Docker objects.

## Start and validate hcorral

Select the copied volume and the original home explicitly. Reproduce all
required extra mounts with hcorral's `--volume`/`-f` options. This example uses
headless mode for the first validation; choose GUI forwarding explicitly after
the state has been checked:

```bash
export HCORRAL_CONTAINER_HOME=/observed/container/home
hcorral --harness codex --image "$image" --state-volume "$new_volume" --no-gui up -d
hcorral --state-volume "$new_volume" info --format=json
hcorral --state-volume "$new_volume" exec id
hcorral --state-volume "$new_volume" exec codex --version
hcorral --state-volume "$new_volume" attach
```

Keep the same workspace, image, home and overlay configuration on subsequent
commands. `up -d` returning does not by itself prove that entrypoint setup and
the tmux session have finished; `attach` waits for readiness and reports startup
failure. Before retiring myCodex, verify:

- Actual state/workspace mounts and runtime UID/GID match the intended values.
- First and newly created panes have an interactive login shell, completion and
  the expected user customization, without permission errors.
- Existing `.bashrc`, chosen login startup file, symlinks and custom tools retain
  their content and ownership. A myCodex shared-default stub can use its existing
  `/etc/skel/.bashrc` fallback when `/etc/mycodex/bashrc` is absent.
- Codex uses the expected configuration and login, lists the expected sessions,
  and resumes a chosen session by its existing ID. Check external SQLite paths,
  attachments and other extra mounts where present.
- `notices` reopens startup information, and repeated attachment preserves the
  running container and tmux panes.

Custom state volumes remain user managed. Hcorral does not relabel the selected
legacy/copy volume, and normal teardown retains it. Save the chosen hcorral
configuration using its ordinary documented settings; an alias called
`myCodex` does not provide command or environment compatibility.

## Recovery and evidence

To abandon a copied-home trial, use hcorral with the same selection to run plain
`down`, retain the trial volume for inspection, and recreate the old environment
through the original myCodex launcher/configuration against its original home
and recorded image. Select that image deliberately; a moving alias may have
changed since the inventory. Do not copy a modified trial home over the original
as an automatic rollback. After direct reuse, returning to an older Codex version
requires data-format compatibility evidence or restoration of the saved copy.

`tests/qualification/mycodex-transition.sh` exercises this procedure using the
real launcher and image recipe from myCodex commit
`ebc930ac00adea662789d6c2f43666ec1003eca0`, with Codex 0.160.0 and optional unrelated
agent installations disabled. It uses only synthetic credentials/conversations
and disposable workspaces/volumes. It checks running/stopped legacy refusal,
copied and explicitly reused homes, preserved file hashes/modes/owners/symlinks,
unchanged volume labels, shell customization, native picker/resume and return
through the old launcher. No real user-state transition is performed by CI.

The fixture runs on both native Linux architectures through mixed-version
qualification. Its test definition is not a passing result; consult
`implementation-status.md` and the relevant CI run. Passing it establishes the
tested named-volume layout and versions, not arbitrary custom mounts, UID
changes, macOS migration or every future Codex downgrade.
