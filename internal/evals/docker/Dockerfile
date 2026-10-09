# Builds the two documentation-variant images from the repository (build context = repository root).
# Both variants run the same binaries; the harness removes the PiG documentation from the without_docs run's home
# (internal/evals/harness_run.go) and the without_docs prompt loses its documentation section.
ARG GO_VERSION=1.27.1
FROM golang:${GO_VERSION}-bookworm AS builder
WORKDIR /src
# Build against the repository mount; only the staged binaries persist.
RUN --mount=type=bind,source=.,target=/src,rw \
    --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -o /opt/pig-evals/pig ./cmd/pig \
    && CGO_ENABLED=0 go build -o /opt/pig-evals/pig-eval-extension ./cmd/pig-eval-extension \
    && CGO_ENABLED=0 go build -o /opt/pig-evals/pig-evals ./cmd/pig-evals

FROM debian:bookworm-slim AS evaluator
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tmux coreutils \
    && rm -rf /var/lib/apt/lists/*
COPY --from=builder /opt/pig-evals /opt/pig-evals
# The runner starts as root so it can hold the evaluators. Before prompting the model, the harness starts the agent as
# UID 65532. Keep the runner (judges, fixtures, prompts) root-only so model tools and generated code cannot read it.
RUN chmod 0755 /opt/pig-evals /opt/pig-evals/pig /opt/pig-evals/pig-eval-extension \
    && chmod 0700 /opt/pig-evals/pig-evals \
    && mkdir -p /run/pi-eval-secrets /repo/packages/evals \
    && chmod 0700 /run/pi-eval-secrets
ENV PI_EVAL_PIG=/opt/pig-evals/pig PI_EVAL_EXTENSION=/opt/pig-evals/pig-eval-extension
WORKDIR /repo/packages/evals
ENTRYPOINT ["/opt/pig-evals/pig-evals", "entrypoint"]

FROM evaluator AS without_docs
ENV PI_EVAL_VARIANT=without_docs

FROM evaluator AS with_docs
ENV PI_EVAL_VARIANT=with_docs
