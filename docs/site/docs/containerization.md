# Containerization

PiG runs with the permissions of its process. Use a container or virtual machine when you need an operating-system security boundary.

A container can limit filesystem, process, device, and network access. It does not make model output or extension source trustworthy.

## Build a PiG image

PiG has no published container image yet. Build from the source tree with a multi-stage Dockerfile:

```dockerfile
FROM golang:1.27.1 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -o /out/pig ./cmd/pig

FROM debian:bookworm-slim
RUN useradd --create-home --uid 10001 pig
COPY --from=build /out/pig /usr/local/bin/pig
USER pig
WORKDIR /workspace
ENTRYPOINT ["pig"]
```

Pin base images by digest for a release build. Generate an SBOM and scan the final image before publication.

## Preserve state deliberately

PiG stores user state below:

```text
~/.pig
```

Mount a named volume when Sessions and settings must survive container replacement:

```bash
docker run --rm -it \
  -v pig-home:/home/pig/.pig \
  -v "$PWD:/workspace" \
  pig-local
```

Mounting your host `~/.pig` gives the container access to host credentials, Sessions, settings, Packages, and extension state. Use a separate volume when that access is not required.

## Workspace access

Mount only the project paths that PiG needs. Use a read-only mount when the agent should inspect but not modify files:

```bash
docker run --rm -it \
  -v "$PWD:/workspace:ro" \
  pig-local -p "Review this repository"
```

A read-only workspace does not prevent access to another writable mount.

## Network access

Disable network access when the task does not need model or external service calls:

```bash
docker run --rm -it --network none pig-local
```

Normal model inference requires network access unless the model endpoint is available through an explicitly permitted local network.

Use network policy or a proxy when the container must reach only approved endpoints.

## Extensions in containers

Source extensions can require a compiler or interpreter. Include only the required runtime in the image, or use a prebuilt Piglet Binary.

A PiG Standard Binary compiles all selected extensions into the executable. It still needs external model services, secrets, and configuration that the Piglet record declares.

Process isolation inside a container is not the same as isolation from the container. A subprocess extension normally has the same container permissions as PiG.

## Piglet agent environments

A Piglet can declare an `agentEnv` requirement for the whole application. This requirement is separate from the code-execution sandbox and from extension process placement.

PiG must enter or verify the required environment before startup. It must not silently use the host when the requirement cannot be met.

See [Piglets](/docs/latest/piglets#required-agent-environment).

## Secrets

Inject secrets at runtime. Do not bake them into an image or copy them into a build layer.

Use:

- environment variables;
- owner-only mounted files;
- an approved external secret resolver.

Review image history and generated SBOMs before release.

## Rootless execution

Run as a non-root user unless the task requires a specific privileged operation. Do not grant broad host mounts, Docker socket access, device access, or additional Linux capabilities by default.

## Related documentation

- [Security](/docs/latest/security)
- [Piglets](/docs/latest/piglets)
- [Piglet Binaries](/docs/latest/piglet-binaries)
- [Extensions](/docs/latest/extensions)
