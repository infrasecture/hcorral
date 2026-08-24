# ADR 0003: Compose authority

Status: accepted

The built-in Compose definition is compiled into the binary and cannot be
replaced in 1.0. Ordered overlays may extend the project, including sidecars,
but the rendered model must preserve hcorral-owned identity, image, mounts,
workdir, GUI boundary, and direct-launch guard. Compose remains the orchestrator.
