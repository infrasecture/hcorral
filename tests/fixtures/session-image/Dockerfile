FROM alpine:3.21@sha256:48b0309ca019d89d40f670aa1bc06e426dc0931948452e8491e3d65087abc07d

# Test-only flock holds a real kernel lock independently of the launcher.
# No Codex, Python, tmux or image-installed transfer helper is supplied.
RUN apk add --no-cache util-linux
VOLUME /unrelated-image-volume
ENTRYPOINT ["/bin/sh", "-c", "echo 'unexpected image entrypoint' >&2; exit 99"]
