# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e
FROM mcr.microsoft.com/devcontainers/base:ubuntu-24.04@sha256:d94c97dd9cacf183d0a6fd12a8e87b526e9e928307674ae9c94139139c0c6eae AS common

ARG DEBIAN_FRONTEND=noninteractive
ARG TARGETARCH
ARG HCORRAL_NODE_VERSION=22.23.2
ARG HCORRAL_NODE_SHA256_AMD64=d60acfe00a2932254bb0ad20e01b0d74397a0875595de719654b214f4b03f307
ARG HCORRAL_NODE_SHA256_ARM64=fff4078c5def658577f92c88db7db3bc0072924bfb93fe52c1e744a54e94abb8
ARG HCORRAL_IMAGE_REVISION=1
ARG HCORRAL_SOURCE_REVISION=unknown
ARG HCORRAL_BUILD_INPUT_DIGEST=unknown

SHELL ["/bin/bash", "-lc"]
USER root
ENV NPM_CONFIG_PREFIX=/usr/local \
    LANG=en_US.UTF-8 \
    LC_ALL=en_US.UTF-8 \
    EDITOR=vi \
    TERM=xterm-256color \
    HCORRAL_CONTAINER_HOME=/home/hcorral \
    HCORRAL_WORKDIR=/work

RUN apt-get update && apt-get install -y --no-install-recommends \
      autoconf automake bash-completion bat build-essential byobu ca-certificates cargo clang clang-format clang-tidy cmake curl \
      dnsutils fd-find fzf g++ gcc gdb gh git git-lfs gosu htop iproute2 iputils-ping jq libtool locales lsof \
      make man-db manpages manpages-dev meson net-tools ninja-build openssh-client p7zip-full rustc \
      pkg-config python-is-python3 python3 python3-dev python3-pip python3-venv ripgrep rsync \
      shellcheck socat strace sudo tmux tree unzip vim wget xz-utils yq zip zstd \
    && locale-gen en_US.UTF-8 \
    && update-locale LANG=en_US.UTF-8 \
    && ln -sf "$(command -v fdfind)" /usr/local/bin/fd \
    && ln -sf "$(command -v batcat)" /usr/local/bin/bat \
    && rm -rf /var/lib/apt/lists/* /tmp/*

# Node is a common runtime dependency for Pi and a useful workstation tool.
# The exact upstream archive is architecture checked and digest verified.
RUN case "${TARGETARCH}" in \
      amd64) node_arch=x64; node_sha256="${HCORRAL_NODE_SHA256_AMD64}" ;; \
      arm64) node_arch=arm64; node_sha256="${HCORRAL_NODE_SHA256_ARM64}" ;; \
      *) echo "unsupported target architecture: ${TARGETARCH}" >&2; exit 2 ;; \
    esac \
    && archive="node-v${HCORRAL_NODE_VERSION}-linux-${node_arch}.tar.xz" \
    && curl --fail --silent --show-error --location --output "/tmp/${archive}" "https://nodejs.org/dist/v${HCORRAL_NODE_VERSION}/${archive}" \
    && printf '%s  %s\n' "${node_sha256}" "/tmp/${archive}" | sha256sum --check --strict \
    && tar --extract --xz --file "/tmp/${archive}" --directory /usr/local --strip-components=1 \
    && rm -f "/tmp/${archive}" \
    && node --version | grep -Fx "v${HCORRAL_NODE_VERSION}" \
    && npm config set fund false \
    && npm config set audit false \
    && npm config set update-notifier false \
    && npm config set prefix /usr/local

RUN mkdir -p /etc/hcorral /usr/share/doc/hcorral \
    && printf '%s\n' 'export NPM_CONFIG_PREFIX="$HOME/.local/share/npm"' 'export PATH="$HOME/.local/bin:$NPM_CONFIG_PREFIX/bin:$HOME/.cargo/bin:$PATH"' >/etc/profile.d/hcorral-path.sh \
    && chmod 0644 /etc/profile.d/hcorral-path.sh \
    && sed -i '/^#if ! shopt -oq posix; then$/,/^#fi$/ s/^#//' /etc/bash.bashrc

COPY image/entrypoint.sh /usr/local/bin/entrypoint.sh
COPY image/session-init.sh /usr/local/bin/hcorral-session-init
COPY LICENSE THIRD_PARTY_LICENSES.md /usr/share/doc/hcorral/
RUN chmod 0755 /usr/local/bin/entrypoint.sh /usr/local/bin/hcorral-session-init \
    && chmod 0644 /usr/share/doc/hcorral/LICENSE /usr/share/doc/hcorral/THIRD_PARTY_LICENSES.md

LABEL org.opencontainers.image.revision="${HCORRAL_SOURCE_REVISION}" \
      org.opencontainers.image.source="https://github.com/infrasecture/hcorral" \
      ai.infrasecture.hcorral.image.revision="${HCORRAL_IMAGE_REVISION}" \
      ai.infrasecture.hcorral.build.input-digest="${HCORRAL_BUILD_INPUT_DIGEST}" \
      ai.infrasecture.hcorral.image.runtime-schema="1"

ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD []

FROM common AS codex
ARG TARGETARCH
ARG HCORRAL_CODEX_VERSION=0.149.1
ARG HCORRAL_CODEX_SHA256_AMD64=e24fb784c7d71140d67afb620f56e9137496cf7f6c9e19217fa3666dcf306278
ARG HCORRAL_CODEX_SHA256_ARM64=14df6802e39a956de994e844b90d51d8254bcc8057b6e66f0f3e3b8f7e2da5b0
ARG HCORRAL_IMAGE_REVISION=1
RUN case "${TARGETARCH}" in \
      amd64) codex_arch=x86_64; codex_sha256="${HCORRAL_CODEX_SHA256_AMD64}" ;; \
      arm64) codex_arch=aarch64; codex_sha256="${HCORRAL_CODEX_SHA256_ARM64}" ;; \
      *) echo "unsupported Codex target architecture: ${TARGETARCH}" >&2; exit 2 ;; \
    esac \
    && archive="codex-${codex_arch}-unknown-linux-musl.tar.gz" \
    && curl --fail --silent --show-error --location --output "/tmp/${archive}" "https://github.com/openai/codex/releases/download/rust-v${HCORRAL_CODEX_VERSION}/${archive}" \
    && printf '%s  %s\n' "${codex_sha256}" "/tmp/${archive}" | sha256sum --check --strict \
    && tar --extract --gzip --file "/tmp/${archive}" --directory /tmp \
    && install -m 0755 "/tmp/codex-${codex_arch}-unknown-linux-musl" /usr/local/bin/codex \
    && rm -f "/tmp/${archive}" "/tmp/codex-${codex_arch}-unknown-linux-musl" \
    && codex --version | grep -F "${HCORRAL_CODEX_VERSION}" \
    && codex completion bash >/etc/bash_completion.d/codex \
    && bash -n /etc/bash_completion.d/codex
RUN printf '%s\n\n%s\n  %s\n' 'Codex container session is running.' 'Run Codex:' 'codex' >/etc/hcorral/session-banner.txt
LABEL org.opencontainers.image.version="${HCORRAL_CODEX_VERSION}-r${HCORRAL_IMAGE_REVISION}" \
      ai.infrasecture.hcorral.harness.type="codex" \
      ai.infrasecture.hcorral.harness.version="${HCORRAL_CODEX_VERSION}" \
      ai.infrasecture.hcorral.codex.version="${HCORRAL_CODEX_VERSION}"
ENV HCORRAL_IMAGE_HARNESS=codex

FROM common AS claude
ARG HCORRAL_CLAUDE_VERSION=2.1.241
ARG HCORRAL_IMAGE_REVISION=1
# Anthropic's supported installer validates the release manifest checksum. It
# installs under an image-owned home; runtime-user updates go to persisted HOME.
RUN mkdir -p /opt/claude \
    && curl --fail --silent --show-error --location https://claude.ai/install.sh -o /tmp/install-claude.sh \
    && HOME=/opt/claude bash /tmp/install-claude.sh "${HCORRAL_CLAUDE_VERSION}" \
    && rm -f /tmp/install-claude.sh \
    && ln -s /opt/claude/.local/bin/claude /usr/local/bin/claude \
    && claude --version | grep -F "${HCORRAL_CLAUDE_VERSION}"
RUN printf '%s\n\n%s\n  %s\n' 'Claude container session is running.' 'Run Claude:' 'claude' >/etc/hcorral/session-banner.txt
LABEL org.opencontainers.image.version="${HCORRAL_CLAUDE_VERSION}-r${HCORRAL_IMAGE_REVISION}" \
      ai.infrasecture.hcorral.harness.type="claude" \
      ai.infrasecture.hcorral.harness.version="${HCORRAL_CLAUDE_VERSION}" \
      ai.infrasecture.hcorral.claude.version="${HCORRAL_CLAUDE_VERSION}"
ENV HCORRAL_IMAGE_HARNESS=claude DISABLE_AUTOUPDATER=1

FROM common AS pi
ARG HCORRAL_PI_VERSION=0.84.2
ARG HCORRAL_IMAGE_REVISION=1
RUN npm install --global --ignore-scripts "@earendil-works/pi-coding-agent@${HCORRAL_PI_VERSION}" \
    && pi --version | grep -F "${HCORRAL_PI_VERSION}"
RUN printf '%s\n\n%s\n  %s\n' 'Pi container session is running.' 'Run Pi:' 'pi' >/etc/hcorral/session-banner.txt
LABEL org.opencontainers.image.version="${HCORRAL_PI_VERSION}-r${HCORRAL_IMAGE_REVISION}" \
      ai.infrasecture.hcorral.harness.type="pi" \
      ai.infrasecture.hcorral.harness.version="${HCORRAL_PI_VERSION}" \
      ai.infrasecture.hcorral.pi.version="${HCORRAL_PI_VERSION}"
ENV HCORRAL_IMAGE_HARNESS=pi
