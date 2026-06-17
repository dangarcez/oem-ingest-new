# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26

FROM docker.io/library/golang:${GO_VERSION}-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG VERSION=dev
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/oem-ingest \
    ./cmd/oem-ingest

RUN mkdir -p /out/app/configs /out/app/auth && cp -R configs/. /out/app/configs/

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

ENV OEM_CONFIG_TARGETS=/app/configs/configTargets.yaml \
    OEM_CONFIG_METRICS=/app/configs/configMetrics.yaml \
    OEM_VALIDATED_CONFIG_OUTPUT=/app/configs/configTargets.validated.yaml \
    OEM_VALIDATION_REPORT_OUTPUT=/app/configs/configTargets.validated.report.yaml

COPY --from=build --chown=nonroot:nonroot /out/oem-ingest ./oem-ingest
COPY --from=build --chown=nonroot:nonroot /out/app/configs ./configs
COPY --from=build --chown=nonroot:nonroot /out/app/auth ./auth

USER nonroot:nonroot

ENTRYPOINT ["/app/oem-ingest"]
CMD ["--help"]
