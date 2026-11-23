FROM golang:1.27-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /workload-identity-sidecar .

FROM ghcr.io/cujarrett/base-distroless:static-debian13@sha256:723f8ed3b1060cf54014601bbb883e79f184bb949dcf85e19b209a69d6979ed9

COPY --from=builder /workload-identity-sidecar /usr/local/bin/workload-identity-sidecar

ENTRYPOINT ["/usr/local/bin/workload-identity-sidecar"]
