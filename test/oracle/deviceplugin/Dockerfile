FROM docker.io/library/golang:1.26.0-alpine3.23@sha256:d4c4845f5d60c6a974c6000ce58ae079328d03ab7f721a0734277e69905473e5 AS build

WORKDIR /source
COPY go.mod go.sum ./
RUN go mod download
COPY test/oracle/deviceplugin ./test/oracle/deviceplugin
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/kasim-device-plugin-oracle \
    ./test/oracle/deviceplugin

FROM scratch

COPY --from=build /out/kasim-device-plugin-oracle /kasim-device-plugin-oracle
USER 0:0
ENTRYPOINT ["/kasim-device-plugin-oracle"]
