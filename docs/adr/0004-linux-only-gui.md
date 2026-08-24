# ADR 0004: Linux-only GUI forwarding

Status: accepted

X11 and Wayland forwarding is permanently Linux-only. macOS supports headless
Linux containers and rejects every non-headless GUI request before Docker.
Linux exposure is opt-in and restricted to one socket plus the minimum X11
credential where applicable.
