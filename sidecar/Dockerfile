FROM golang:1.27-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /workload-identity-sidecar .

# Alpine, not distroless, because the Api composition's startup probe runs sh in this container.
FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a

COPY --from=builder /workload-identity-sidecar /usr/local/bin/workload-identity-sidecar

RUN addgroup -S app && adduser -S app -G app
# Numeric, not the name. The kubelet cannot verify runAsNonRoot for a user it cannot resolve.
USER 100

ENTRYPOINT ["/usr/local/bin/workload-identity-sidecar"]
