# SPDX-License-Identifier: MIT

ARG GO_IMAGE=golang:1.27.3-alpine3.24@sha256:f2c872bcee949b7be9c490efd02b48a8a2fbf947cdf533f1c58525ec9d8170fd

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
