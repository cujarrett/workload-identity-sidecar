# Run before pushing
ci: lint test build

lint:
    cd sidecar/secret-fetcher && go mod tidy -diff
    cd sidecar/secret-fetcher && golangci-lint run

test:
    cd sidecar/secret-fetcher && go test -race ./...

build:
    cd sidecar/secret-fetcher && go build -o secret-fetcher .

run:
    cd sidecar/secret-fetcher && go run .

# Build the sidecar image locally, the way CI builds it
image:
    docker build --platform linux/arm64 -t workload-identity-sidecar:dev sidecar
