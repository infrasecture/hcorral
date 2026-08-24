# ADR 0003: Compose authority

Status: accepted

The built-in Compose definition is compiled into the binary, is always first,
and cannot be replaced wholesale in 1.0. Ordered overlays, sidecars, and CLI
mounts are unrestricted trusted Compose input and may override every built-in
field. Hcorral diagnoses the final rendered result but does not impose overlay
policy. Exact ownership checks still guard existing resources before mutation.
