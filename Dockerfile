# The echo mailbox (cmd/dromas-echo): a client on a published address that
# answers every message passing every check with a signed reply carrying
# the body back, with the dromas command beside it for the operator (addr,
# adopt). How it runs, its state in a tmpfs and its identity key mounted in:
# cmd/dromas-echo.
#
# The Go stage runs on the builder's own platform and cross-compiles for the
# target's (docker build --platform linux/amd64,linux/arm64), so no stage
# runs under emulation: the image it is copied into only copies.
#
# The build context holds .git, so go build stamps each binary with the
# module's version from the repository (Go 1.24: the tag at a tagged commit,
# a pseudo-version after it), and every request names that version in
# Dromas-Version (client.Version). Without it a binary names 0.0.0-devel,
# which any server's version floor refuses. A checkout needs its tags for the
# pseudo-version to follow the last release (docker-publish.yml).
FROM --platform=$BUILDPLATFORM golang:1.27.3-trixie@sha256:af1bfc2614ebb1dbd31af514b78740fd678850e76498762b425ecee4c5c6ea80 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/ ./cmd/dromas-echo ./cmd/dromas

# Debian slim. Each base image is an exact tag and its index digest, which
# Dependabot moves together.
# Nothing runs in it but the binaries, as uid 65532; the CA bundle comes from
# the build stage, for TLS to the server.
FROM debian:13.7-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/dromas-echo /out/dromas /usr/local/bin/
USER 65532:65532
ENTRYPOINT ["dromas-echo"]
