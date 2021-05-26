FROM golang:1.16 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY checker/go.mod go.mod
COPY checker/go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY checker/checker.go checker.go
COPY checker/pkg/ pkg/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o checker checker.go

# Use distroless as minimal base image to package the checker binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/checker /bin/
USER nonroot:nonroot

ENTRYPOINT ["checker"]