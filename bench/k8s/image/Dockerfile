# syntax=docker/dockerfile:1.7
# The PiG benchmark image for bench/k8s: PiG's Durable builds (TinyGo and Go Wasm cores, the native Go Durable
# benchmark), Node, pi-durable at PiG's pinned Pi version from bench/k8s/image/pi-durable's lockfile, and the Pi
# reference checkout pinned in durable/contract/PIN. Build it with bench/k8s/image/build.sh, which passes the commit and prints the digest-pinned
# reference the runner needs. Every download is pinned by version and sha256; the base images are pinned by digest.
ARG GO_IMAGE=golang:1.27.1-bookworm@sha256:6e725de5c0593b829d80b04f169d4fe119b32c612744c79e3311acf14b738e7f
ARG RUNTIME_IMAGE=debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587

FROM ${GO_IMAGE} AS build
ARG TARGETARCH
ARG NODE_VERSION=24.19.0
ARG NODE_SHA256_AMD64=f625d97cd707df4ff96254916fbc5ff014f09c09effe5a1e0ca8f6d41a8789d4
ARG NODE_SHA256_ARM64=d28c8a5bf0a808f0ed434a1dce8c54ae98f0371c0bd86ac58abc613f73e6643f
ARG TINYGO_VERSION=0.42.0
ARG TINYGO_SHA256_AMD64=b87688fa2e19cee7d813cad7fd7dadb71dff3198e47125aba66ba4af5e490438
ARG TINYGO_SHA256_ARM64=f2f3f863e63728b87772e7306bd6030df6e6c89773a7082eb0122113cf9e453d
RUN set -eu; \
	case "${TARGETARCH:-amd64}" in \
		amd64) nodearch=x64; nodesum=$NODE_SHA256_AMD64; tgsum=$TINYGO_SHA256_AMD64 ;; \
		arm64) nodearch=arm64; nodesum=$NODE_SHA256_ARM64; tgsum=$TINYGO_SHA256_ARM64 ;; \
		*) echo "unsupported architecture ${TARGETARCH}" >&2; exit 1 ;; \
	esac; \
	curl -fsSLo /tmp/node.tar.gz "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-${nodearch}.tar.gz"; \
	echo "${nodesum}  /tmp/node.tar.gz" | sha256sum -c -; \
	mkdir -p /usr/local/node; tar -xzf /tmp/node.tar.gz -C /usr/local/node --strip-components=1; \
	curl -fsSLo /tmp/tinygo.tar.gz "https://github.com/tinygo-org/tinygo/releases/download/v${TINYGO_VERSION}/tinygo${TINYGO_VERSION}.linux-${TARGETARCH:-amd64}.tar.gz"; \
	echo "${tgsum}  /tmp/tinygo.tar.gz" | sha256sum -c -; \
	tar -xzf /tmp/tinygo.tar.gz -C /usr/local; \
	rm /tmp/node.tar.gz /tmp/tinygo.tar.gz
ENV PATH=/usr/local/node/bin:/usr/local/tinygo/bin:$PATH \
	GOTOOLCHAIN=local GOWORK=off GOFLAGS=-buildvcs=false CGO_ENABLED=0
WORKDIR /src
COPY . .
ARG PIG_COMMIT
# Built outputs go under /opt/pig: bin/ (k8sbench, durableperf, the core's native bench when the source has one),
# dist/ (core-go.wasm, core-tinygo.wasm), pi-durable/ (durable/interop/bench.mjs beside the locked reference
# packages), COMMIT, VERSIONS and ARTIFACTS.sha256.
RUN --mount=type=cache,target=/root/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build set -eu; \
	test -n "${PIG_COMMIT}" || { echo "PIG_COMMIT is required (bench/k8s/image/build.sh sets it)" >&2; exit 1; }; \
	mkdir -p /opt/pig/bin /opt/pig/dist /opt/pig/pi-durable; \
	go build -trimpath -o /opt/pig/bin/k8sbench ./bench/k8s/cmd/k8sbench; \
	go build -trimpath -o /opt/pig/bin/durableperf ./bench/durableperf; \
	if [ -f durable/core/Makefile ]; then \
		make -C durable/core build-go build-tinygo; \
	else \
		sh durable/core/budget/build-wasm.sh go durable/core/cmd/corewasm durable/core/host/cf/dist/core-go.wasm; \
		sh durable/core/budget/build-wasm.sh tinygo durable/core/cmd/corewasm durable/core/host/cf/dist/core-tinygo.wasm; \
	fi; \
	cp durable/core/host/cf/dist/core-go.wasm durable/core/host/cf/dist/core-tinygo.wasm /opt/pig/dist/; \
	if [ -d durable/core/sqlhost/cmd/bench ]; then go build -trimpath -o /opt/pig/bin/sqlhost-bench ./durable/core/sqlhost/cmd/bench; fi; \
	cp bench/k8s/image/pi-durable/package.json bench/k8s/image/pi-durable/package-lock.json durable/interop/bench.mjs /opt/pig/pi-durable/; \
	(cd /opt/pig/pi-durable && npm ci --ignore-scripts --no-audit --no-fund --loglevel=error); \
	echo "${PIG_COMMIT}" > /opt/pig/COMMIT; \
	{ echo "go=$(go env GOVERSION)"; echo "tinygo=$(tinygo version | awk '{print $3}')"; echo "node=$(node --version)"; \
	  echo "pi-durable=$(node -p 'require("/opt/pig/pi-durable/node_modules/@earendil-works/pi-durable/package.json").version')"; \
	  echo "pi-reference=$(awk '$1 == "main" { print $2 }' durable/contract/PIN)"; echo "arch=${TARGETARCH:-amd64}"; \
	  echo "builder=bench/k8s/image/Dockerfile"; } > /opt/pig/VERSIONS; \
	(cd /opt/pig && sha256sum bin/* dist/* pi-durable/bench.mjs pi-durable/package-lock.json) > /opt/pig/ARTIFACTS.sha256

# The Pi reference at the commit durable/contract/PIN pins, with its dependencies, for profiles that run it.
# PI_REFERENCE=0 leaves it out.
ARG PI_REFERENCE=1
RUN set -eu; mkdir -p /opt/pi; \
	if [ "${PI_REFERENCE}" = 1 ]; then DURABLE_CONTRACT_CACHE=/opt/pi sh durable/contract/tools/setup.sh; fi

# The artifact bundle for k8sbench -bundle (build.sh with BUNDLE=DIR exports it to a local directory): the same files
# as /opt/pig, run on the official Node image, so a cluster without a registry needs no custom image.
FROM scratch AS bundle
COPY --from=build /opt/pig /

FROM ${RUNTIME_IMAGE}
COPY --from=build /usr/local/node /usr/local/node
COPY --from=build /opt/pig /opt/pig
COPY --from=build /opt/pi /opt/pi
# taskset comes from util-linux, which the Debian base includes.
RUN command -v taskset >/dev/null
ENV PATH=/opt/pig/bin:/usr/local/node/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
USER 65532:65532
WORKDIR /data
