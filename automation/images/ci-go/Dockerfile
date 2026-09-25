# SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
# SPDX-License-Identifier: MIT

ARG GO_IMAGE=golang:1.27.1-alpine3.24@sha256:4cb7ac979db5fcc41cae44b2227ba5ab8a51e8807f40d9ba4dee20a0ad960b5b

FROM ${GO_IMAGE}
USER root
COPY automation/images/ci-go/packages.lock /tmp/packages.lock
RUN apk update \
  && xargs apk add --no-cache < /tmp/packages.lock \
  && awk 'BEGIN { RS=""; FS="\n" } { p=""; v=""; for (i=1; i<=NF; i++) { if ($i ~ /^P:/) p=substr($i,3); if ($i ~ /^V:/) v=substr($i,3) } if (p != "") print p "=" v }' /lib/apk/db/installed | LC_ALL=C sort > /tmp/installed.lock \
  && cmp /tmp/packages.lock /tmp/installed.lock \
  && rm /tmp/packages.lock /tmp/installed.lock \
  && mkdir -p /workspace

ENV GOCACHE=/tmp/pig-go-build
ENV GOMODCACHE=/tmp/pig-go-mod
ENV GOPATH=/tmp/pig-go

WORKDIR /workspace
ENTRYPOINT []
CMD ["bash", "-lc", "go version && git --version"]
