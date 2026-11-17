# syntax=docker/dockerfile:1
# Multi-arch (linux/amd64 + linux/arm64) image holding the single simplemonitor binary.
# The eBPF object (internal/agent/flows/flows_bpfel.o) is committed, so no clang is needed here.

FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/simplemonitor ./cmd/simplemonitor

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/simplemonitor /simplemonitor
ENTRYPOINT ["/simplemonitor"]
