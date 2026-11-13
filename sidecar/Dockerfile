FROM golang:1.27-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /workload-identity-sidecar .

FROM ghcr.io/cujarrett/base-go:static-debian13@sha256:f0505a0e3fdd29902098d6ef26b5dc5bd206841aa33441bb875b5ccdc117cfe0

COPY --from=builder /workload-identity-sidecar /usr/local/bin/workload-identity-sidecar

ENTRYPOINT ["/usr/local/bin/workload-identity-sidecar"]
