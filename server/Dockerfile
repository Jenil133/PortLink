# Build stage: Debian based because h3-go needs cgo (gcc + glibc). The Go version must satisfy
# go.mod: this image sets GOTOOLCHAIN=local and will not fetch a newer toolchain.
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# -o with a directory builds every command under ./cmd into it: feeder, ingest, gateway, loadgen.
RUN CGO_ENABLED=1 go build -trimpath -o /out/ ./cmd/...

# Runtime stage: must also be glibc based. Do not switch to alpine or scratch.
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/* && useradd -r -u 10001 lens
COPY --from=build /out/ /usr/local/bin/
USER lens
