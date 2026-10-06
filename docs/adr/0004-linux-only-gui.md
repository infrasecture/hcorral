# ADR 0004: Linux-only GUI forwarding

Status: accepted

X11 and Wayland forwarding is permanently Linux-only. macOS supports headless
Linux containers and rejects every non-headless GUI request before Docker.
Linux creation automatically discovers a usable local Wayland display, then
X11, and otherwise succeeds headless. Exposure remains restricted to one socket
plus the minimum X11 credential where applicable. Automatic forwarding is off
over SSH and requires a verified native local Docker engine; a Unix endpoint
alone does not qualify Docker Desktop. Explicit GUI requests fail if unavailable.

Existing containers retain their deployed mode. Changing it requires explicit
reconciliation. Detection reads facts; credential installation happens only
when applying a configuration. This replaces the original opt-in default to
match myCodex PR #17 while preserving the Linux-only transport boundary.
