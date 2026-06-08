# Build the manager binary
FROM golang:1.26.4@sha256:68cb6d68bed024785b69195b89af7ac7a444f27791435f98647edff595aa0479 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd/main.go cmd/main.go
COPY api/ api/
COPY internal/ internal/

# Build
# the GOARCH has a default value of amd64 if not provided
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -a -o manager cmd/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static@sha256:3592aa8171c77482f62bbc4164e6a2d141c6122554ace66e5cc910cadb961ff0
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
