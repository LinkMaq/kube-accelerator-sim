FROM golang:1.25-alpine AS build

WORKDIR /src
COPY main.go .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/native-reconciler main.go

FROM scratch
COPY --from=build /out/native-reconciler /native-reconciler
USER 65532:65532
ENTRYPOINT ["/native-reconciler"]
