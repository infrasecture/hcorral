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

Bare launch attaches without reconciliation when the selected container is
running. Pull, recreate, and drift reconciliation are always explicit.
