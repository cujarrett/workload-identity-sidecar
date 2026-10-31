# Run before pushing
ci: lint test build

lint:
    cd sidecar && go mod tidy -diff
    cd sidecar && golangci-lint run

test:
    cd sidecar && go test -race ./...

build:
    cd sidecar && go build -race -o workload-identity-sidecar .

run:
    cd sidecar && go run .

# Build the sidecar image locally, the way CI builds it
image:
    docker build --platform linux/arm64 -t workload-identity-sidecar:dev sidecar
