# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT

ARG GO_BUILDER=cgr.dev/chainguard/go:latest-dev@sha256:72f9b391f511f88e7154aaaa5ee1e27fb6594575d9a406940f49786b30172803
ARG WOLFI_BASE=cgr.dev/chainguard/wolfi-base:latest@sha256:a31344ab2cb8618db84f535eec56f76f6178b142cb92cb2e48676cc2dcebea72

# GO_VERSION must equal the go.mod toolchain; make compliance checks it.
ARG GO_VERSION=1.27.1

FROM ${GO_BUILDER} AS go-toolchain
ARG TARGETARCH
ARG GO_VERSION
ARG GO_SHA256_AMD64=63d339f0da5ab53635a56f2490a7984dfe12dfcff22ad749f63edaf590168445
ARG GO_SHA256_ARM64=3450b45a3f9ee8568792736a5c5e70a1f2e9b36c35a8f74958c03e51d7d92bec
RUN case "${TARGETARCH}" in \
      amd64) checksum="${GO_SHA256_AMD64}" ;; \
      arm64) checksum="${GO_SHA256_ARM64}" ;; \
      *) echo "unsupported Go target architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && wget -q "https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz" -O /tmp/go.tar.gz \
    && echo "${checksum}  /tmp/go.tar.gz" | sha256sum -c - \
    && mkdir -p /out \
    && tar -C /out -xzf /tmp/go.tar.gz \
    && test "$(/out/go/bin/go env GOVERSION)" = "go${GO_VERSION}"

FROM ${WOLFI_BASE} AS toolchain-base
COPY automation/images/ci-parity/packages.lock /tmp/packages.lock
RUN apk update \
  && xargs apk add --no-cache < /tmp/packages.lock \
  && awk 'BEGIN { RS=""; FS="\n" } { p=""; v=""; for (i=1; i<=NF; i++) { if ($i ~ /^P:/) p=substr($i,3); if ($i ~ /^V:/) v=substr($i,3) } if (p != "") print p "=" v }' /lib/apk/db/installed | LC_ALL=C sort > /tmp/installed.lock \
  && cmp /tmp/packages.lock /tmp/installed.lock \
  && rm /tmp/packages.lock /tmp/installed.lock

FROM toolchain-base AS pi-oracle
# PI_VERSION must equal coding.UpstreamVersion; make compliance checks it.
ARG PI_VERSION=0.87.1
COPY automation/images/ci-parity/package.json automation/images/ci-parity/package-lock.json /opt/pig/pi/
RUN npm ci --prefix /opt/pig/pi --omit=dev --ignore-scripts --no-audit --no-fund \
  && mkdir -p /opt/pig/pi/bin \
  && ln -s ../node_modules/.bin/pi /opt/pig/pi/bin/pi \
  && test "$(/opt/pig/pi/bin/pi --version)" = "${PI_VERSION}" \
  && node -e 'const [want, ...packages] = process.argv.slice(1); for (const p of packages) { if (require(`/opt/pig/pi/node_modules/@earendil-works/${p}/package.json`).version !== want) process.exit(1) }' "${PI_VERSION}" pi-agent-core pi-ai pi-coding-agent pi-tui \
  && coding_root=/opt/pig/pi/node_modules/@earendil-works/pi-coding-agent \
  && rm -rf /opt/pig/pi/node_modules/@earendil-works/pi-ai/node_modules/@types/node \
  && mkdir -p "${coding_root}/node_modules/@earendil-works" "${coding_root}/node_modules/@types" \
  && ln -s /opt/pig/pi/node_modules/@types/node "${coding_root}/node_modules/@types/node" \
  && for package in pi-agent-core pi-ai pi-tui; do \
       ln -s "/opt/pig/pi/node_modules/@earendil-works/${package}" "${coding_root}/node_modules/@earendil-works/${package}"; \
     done

FROM toolchain-base AS npm-runtime
COPY automation/images/npm-runtime/package.json automation/images/npm-runtime/package-lock.json /opt/pig/npm/
RUN npm ci --prefix /opt/pig/npm --omit=dev --ignore-scripts --no-audit --no-fund \
  && rm -rf \
    /opt/pig/npm/node_modules/npm/node_modules/brace-expansion \
    /opt/pig/npm/node_modules/npm/node_modules/ip-address \
    /opt/pig/npm/node_modules/npm/node_modules/tar \
    /opt/pig/npm/node_modules/npm/node_modules/undici \
  && test "$(node /opt/pig/npm/node_modules/npm/bin/npm-cli.js --version)" = "12.1.0"

FROM toolchain-base
ARG GO_VERSION
USER root
RUN mkdir -p /usr/lib /opt/pig /workspace \
  && ln -sf /usr/bin/fd /usr/local/bin/fdfind \
  && chown -R nonroot:nonroot /opt/pig /workspace
COPY --from=go-toolchain /out/go /usr/lib/go
COPY --from=pi-oracle /opt/pig/pi /opt/pig/pi
COPY --from=npm-runtime /opt/pig/npm /opt/pig/npm
RUN ln -s /opt/pig/npm/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
  && ln -s /opt/pig/npm/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx \
  && test "$(/usr/lib/go/bin/go env GOVERSION)" = "go${GO_VERSION}"

ENV GOCACHE=/tmp/pig-go-build
ENV GOMODCACHE=/tmp/pig-go-mod
ENV GOPATH=/tmp/pig-go
ENV CARGO_HOME=/tmp/pig-cargo
ENV PATH="/usr/lib/go/bin:/opt/pig/pi/bin:${PATH}"
ENV PIG_PARITY_PI_BIN=/opt/pig/pi/bin/pi
ENV PI_PACKAGE_ROOT=/opt/pig/pi/node_modules/@earendil-works/pi-coding-agent

USER nonroot
WORKDIR /workspace
ENTRYPOINT []
CMD ["bash", "-lc", "go version && node --version && npm --version && python3 --version && rustc --version && cargo --version && pi --version && tmux -V"]
