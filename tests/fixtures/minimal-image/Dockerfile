FROM alpine:3.21@sha256:48b0309ca019d89d40f670aa1bc06e426dc0931948452e8491e3d65087abc07d

RUN apk add --no-cache bash tmux xset wayland-utils \
 && printf '#!/bin/sh\nshift\nexec "$@"\n' >/usr/local/bin/gosu \
 && chmod 0755 /usr/local/bin/gosu \
 && ln -s /usr/bin/tmux /usr/local/bin/byobu-tmux

COPY tests/fixtures/minimal-image/session-init.sh /usr/local/bin/hcorral-session-init
COPY tests/fixtures/minimal-image/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod 0755 /usr/local/bin/hcorral-session-init /usr/local/bin/entrypoint.sh

LABEL ai.infrasecture.hcorral.codex.version="0.0.0-test" \
      ai.infrasecture.hcorral.image.revision="1" \
      ai.infrasecture.hcorral.build.input-digest="sha256:0000000000000000000000000000000000000000000000000000000000000000" \
      ai.infrasecture.hcorral.image.runtime-schema="1"

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
