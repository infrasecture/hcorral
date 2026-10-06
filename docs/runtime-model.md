# Runtime model

The physical workspace path is resolved like `pwd -P`. Hcorral computes:

```text
workspace-id = SHA256("ai.infrasecture.hcorral.workspace.v1" NUL path)
corral-id    = SHA256("ai.infrasecture.hcorral.corral.v1" NUL path NUL harness)
```

Generated project/container names are
`hcorral-<basename_slug>-<first7_corral_id>`. Workspace-private state is
`hcorral-<basename_slug>-<first7_workspace_id>`. The readable suffix is not
ownership evidence; full IDs and scheme versions are labels.

The project name is the operational selector for container, lock, session, GUI
credentials, and Compose lifecycle. An explicit `--project-name` can create
multiple instances with one corral ID. Hcorral lists/warns about multiplicity
but targets only the generated or explicitly selected project.

The embedded base Compose definition is materialized from the launcher and is
always first. GUI, user overlays, and generated `-v` overlays follow in order.
The latter are trusted and unrestricted. Existing resources are mutated only
after exact full ownership/Compose labels are verified.

The default global state volume is shared by all harnesses. The private state
volume is shared by all harnesses in one workspace but not other workspaces.
Custom volumes are user managed. Concurrent first initialization of one fresh
shared home can race; start one corral to readiness first when that matters.

Bare launch attaches without pulling or reconciling a running container. It
refreshes `latest` (including an omitted tag) for an inactive project by default.
Named tags and digest references remain pinned. `HCORRAL_AUTO_PULL=false`
disables that refresh; creating a new container still fetches a missing image.

For a stopped container, a successful refresh changes the container only when
the image ID changed and every deployed service's Compose configuration hash
matches the final configuration. Missing overlays, missing hashes, a changed
service set or a changed configuration preserve the original container and
produce a retained report. Deployed GUI sockets and credentials are reused;
automatic refresh does not switch desktop access or restart sidecars.

A failed refresh starts an existing stopped container with its original image
and mounts, even if the local alias points elsewhere. Without a container,
startup can use a cached image after a failed pull, with an explanation; it fails
if neither a pull nor a cached image is usable. Paused, restarting and dead
containers require explicit attention. Ownership and state are inspected again
under the project lock and after pulls. The lock coordinates clients on this
host; it is not a distributed lock across Docker clients on different hosts.

`pull` fetches the image selected by the final Compose overlays without
recreating; `start` starts the original container; `up -d` explicitly reconciles
configuration. Persistently set GUI defaults do not force changes on attachment.

The launcher retains startup/update reports in the tmux session. `hcorral
notices` reopens the last report; it does not start a stopped container or recover
a missing session. Reports use a dismissible, scrollable popup, independent of
pane scrollback. The session's GUI badge describes the deployed configuration.
Custom images need Bash, tmux with `display-popup` and `run-shell -C`, and `less`
for retained reports (the supported Ubuntu image provides tmux 3.4). The launcher
supplies its own helper, so an image-installed notice protocol is unnecessary.

`info` and configuration comparison may materialize content-addressed Compose
assets and temporary mount overlays on the client. They do not initialize
volumes, pull images, reconcile containers or install/replace X11 credentials.
Deployed image facts use Docker's actual image ID, independently of mutable
image references. Installed harness versions can differ from bundled versions
when the persisted user prefix contains an update.

`session export/import` uses the existing Codex corral's inspected identity and
storage, under the project lock, without Compose or GUI preparation. A temporary
helper runs against that storage whether the workstation is running or stopped;
only the helper is started and removed. The host participates through native Go
code and streaming Docker I/O, so its paths need not exist on a remote daemon.
Explicit state selections that disagree with the deployed runtime home are
rejected. Conversation writer locks are separate from the local project lock.
See [the transfer design and remaining qualification gates](session-transfer-design.md).
