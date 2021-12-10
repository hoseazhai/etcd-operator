# Build the manager binary
FROM golang:1.13 as builder

RUN apt-get -y update && apt-get -y install upx

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer


# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/
COPY cmd/ cmd/
COPY pkg/ pkg/

# Build
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
ENV GO111MODULE=on
ENV GOPROXY="https://goproxy.cn"

RUN go mod download && \
    go mod edit -replace github.com/coreos/bbolt@v1.3.4=go.etcd.io/bbolt@v1.3.4 && \
    go mod edit -replace google.golang.org/grpc@v1.29.1=google.golang.org/grpc@v1.26.0 && \
    go mod tidy && \
    go build -a -o manager main.go && \
    go build -a -o backup cmd/backup/main.go && \
    upx manager backup




# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM hoseazhai/distroless:nonroot as manager
WORKDIR /
COPY --from=builder /workspace/manager .
USER nonroot:nonroot
ENTRYPOINT ["/manager"]

# backup taget
FROM hoseazhai/distroless:nonroot as backup
WORKDIR /
COPY --from=builder /workspace/backup .
USER nonroot:nonroot
ENTRYPOINT ["/backup"]