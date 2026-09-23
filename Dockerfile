# Build the manager binary natively on host hardware (bypasses QEMU emulation)
FROM --platform=$BUILDPLATFORM golang:1.24 AS builder

# Buildx automatically passes these platform values
ARG BUILDPLATFORM
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

# Copy the Go Modules manifests
COPY go.mod go.sum ./
# Cache dependencies natively
RUN go mod download

# Copy the Go source (relies on .dockerignore to filter)
COPY . .

# Native Go cross-compilation for target architectures
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -a -o manager cmd/main.go

# Minimal runtime image stage
FROM registry.access.redhat.com/ubi9/ubi-micro:latest
LABEL name="HariKube API-Extension"
LABEL vendor="inspirNation Bt."
LABEL version="beta-v1.0.0-1"
LABEL release="0"
LABEL summary="API Extension to solve missing Kubernetes data capabilities"
LABEL description="This extension implements counting, transactions, advanced filtering, etc."
LABEL maintainer="richard.kovacs@harikube.com"

COPY LICENSE /licenses/LICENSE
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532

ENTRYPOINT ["/manager"]