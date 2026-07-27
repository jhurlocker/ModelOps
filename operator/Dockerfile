FROM registry.access.redhat.com/ubi9/go-toolset:1.22 AS builder

WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download

COPY main.go main.go
COPY api/ api/
COPY internal/ internal/

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o /tmp/manager main.go

FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

RUN microdnf install -y ca-certificates && microdnf clean all

COPY --from=builder /tmp/manager /usr/local/bin/manager

USER 65532:65532

ENTRYPOINT ["/usr/local/bin/manager"]
