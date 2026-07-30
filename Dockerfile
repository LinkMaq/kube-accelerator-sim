FROM --platform=$BUILDPLATFORM golang:1.26.0-alpine3.23@sha256:d4c4845f5d60c6a974c6000ce58ae079328d03ab7f721a0734277e69905473e5 AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG SOURCE_REVISION=unknown
ARG BUILD_DATE=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
    -ldflags="-s -w \
      -X github.com/LinkMaq/kube-accelerator-sim/internal/version.productVersion=${VERSION} \
      -X github.com/LinkMaq/kube-accelerator-sim/internal/version.sourceRevision=${SOURCE_REVISION} \
      -X github.com/LinkMaq/kube-accelerator-sim/internal/version.buildDate=${BUILD_DATE}" \
    -o /out/kasim-controller ./cmd/kasim-controller

FROM gcr.io/distroless/static-debian13:nonroot@sha256:f7f8f729987ad0fdf6b05eeeae94b26e6a0f613bdf46feea7fc40f7bd72953e6

ARG VERSION=dev
ARG SOURCE_REVISION=unknown
ARG BUILD_DATE=unknown

LABEL org.opencontainers.image.title="kube-accelerator-sim controller" \
      org.opencontainers.image.description="Existing-cluster accelerator simulation reconciler" \
      org.opencontainers.image.source="https://github.com/LinkMaq/kube-accelerator-sim" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$SOURCE_REVISION \
      org.opencontainers.image.created=$BUILD_DATE

COPY --from=build /out/kasim-controller /kasim-controller
USER 65532:65532
EXPOSE 8080 8081
ENTRYPOINT ["/kasim-controller"]
