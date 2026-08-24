# Runtime model

The Go launcher resolves a physical workspace, computes its full versioned
SHA-256 identity, checks for a same-workspace myCodex environment, verifies any
existing hcorral resources, and then delegates orchestration to Docker Compose.

The built-in Compose definition is compiled into the binary and materialized as
an immutable content-addressed cache file. Linux GUI and user overlay files are
applied afterward. The fully rendered `hcorral` service is validated before a
mutation.

Bare `hcorral` is attach-first:

1. attach to a matching running container without reconciliation;
2. recover a missing tmux session in place;
3. start a matching stopped container only when desired configuration matches;
4. create a missing environment after pulling the selected image and creating
   the selected state volume.

Image pulls and Compose reconciliation are explicit. Locks serialize local
mutations, while Docker labels remain the ownership authority.
