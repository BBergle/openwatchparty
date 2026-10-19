# owp-relay image. Build context: relay/.
#
#   docker buildx build --platform linux/amd64,linux/arm64 --build-arg VERSION=1.0.0 -t ghcr.io/edde746/owp-relay relay
#
# The build stage runs on the builder's platform and cross-compiles, so a multi-platform build needs no emulation.
# VERSION shows up in /metrics as owp_build_info{version="…"}.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS TARGETARCH VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /out/owp-relay ./cmd/owp-relay \
    && mkdir /out/state

# distroless static: CA certificates (the relay's ACME client needs them), tzdata and a nonroot user (65532), nothing
# else.
FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /out/owp-relay /usr/local/bin/owp-relay
# A fresh volume mounted at /state takes this ownership, so the relay can write its snapshots and certificates there.
COPY --from=build --chown=65532:65532 /out/state /state
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/owp-relay"]
