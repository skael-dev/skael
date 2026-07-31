# The whetstone base image. Contents are the "makes it feel usable" set: what a
# document, data, or workflow skill reaches for without declaring it. Anything
# beyond this is a per-skill layer built from the spec's deps.
#
# Pinned to a distribution release rather than a rolling tag: an eval score is
# attributed to an environment, and a base image that changes underneath makes
# two scores incomparable without saying so.
FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive \
    LANG=C.UTF-8 \
    PIP_BREAK_SYSTEM_PACKAGES=1 \
    NODE_MAJOR=22

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates python3 python3-pip python3-venv \
      pandoc poppler-utils libreoffice-core libreoffice-calc libreoffice-writer \
      ffmpeg jq ripgrep git make unzip tinyproxy curl gnupg \
    && curl -fsSL https://deb.nodesource.com/setup_${NODE_MAJOR}.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && npm install -g @anthropic-ai/claude-code@2.1.220 \
    && apt-get purge -y gnupg && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/*

# Runs execute as a non-root user: a skill that writes to /usr is a skill whose
# violation should be observable, not one that silently succeeds.
#
# ubuntu:24.04 ships a preexisting "ubuntu" user at UID 1000 (added for
# cloud-init); it is removed first so "useradd -u 1000 runner" does not fail
# with "UID 1000 is not unique".
RUN userdel -r ubuntu 2>/dev/null; useradd -m -u 1000 runner && mkdir -p /workspace && chown runner:runner /workspace
WORKDIR /workspace
USER runner
