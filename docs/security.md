# Security model

Hcorral is designed for autonomous development agents and is not a hardened
sandbox. The workspace, state home, and every explicit extra mount are writable
unless a read-only mode is supplied. Agent processes can read credentials and
modify data exposed through those mounts.

Before mutation, hcorral verifies the full workspace identity and runtime
schema labels. Seven-character hashes and readable slugs are display/naming
inputs only. The launcher refuses ambiguous ownership and does not manage
myCodex resources.

Linux GUI access weakens isolation. X11 receives one socket and copied cookie;
Wayland receives one user-owned compositor socket. Hcorral does not forward
D-Bus, audio, GPU devices, host networking, or the whole runtime directory.
GUI forwarding is unsupported on macOS.

Compose overlays may add sidecars and resources but cannot replace hcorral's
image, identity labels, container name, workspace/home/state mounts, workdir,
GUI boundary, or direct-launch guard. They also cannot make the managed service
privileged, select host network/PID/IPC/user namespaces, add devices or
capabilities, disable the image entrypoint, select a runtime user, make the
root filesystem read-only, or weaken security options. Existing non-external
Compose networks and volumes require exact Compose ownership labels before any
mutation; external overlay resources remain explicitly user-managed.
