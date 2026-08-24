# Security model

Hcorral is a convenience boundary, not a hardened sandbox. A user able to run
Docker controls the daemon and effectively the host; persisted home content is
therefore trusted during root bootstrap. Every harness can modify the shared
workspace, and shared state exposes credentials to every container mounting it.

The built-in headless definition exposes no Docker socket, D-Bus, audio, GPU,
host network, or broad runtime directory. Linux GUI mode forwards only the
selected X11 socket and copied cookie, or one owned Wayland socket. macOS GUI
is unsupported. Headless remote contexts and Colima are supported, with bind
paths interpreted by the daemon.

Explicit Compose overlays, sidecars, custom images, and extra mounts are
unrestricted trusted Docker inputs. They can replace the image or entrypoint,
add privileges and host sockets, discard ownership labels, or otherwise remove
every built-in safety property. Hcorral deliberately does not validate them.

Lifecycle operations verify full `ai.infrasecture.hcorral.*` identities before
reusing or deleting managed resources. After tearing down the selected project,
`down -v` deletes its exactly owned workspace-private volume only when no other
running or stopped container references it. Global and custom state are
retained. The read-only myCodex guard never operates legacy resources.
