FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a AS spire

ARG SPIRE_VERSION=1.15.4
ARG TARGETARCH
RUN apk add --no-cache curl tar && \
    curl -sSL "https://github.com/spiffe/spire/releases/download/v${SPIRE_VERSION}/spire-${SPIRE_VERSION}-linux-${TARGETARCH}-musl.tar.gz" \
      -o /tmp/spire.tar.gz && \
    tar -xzf /tmp/spire.tar.gz -C /tmp && \
    mv /tmp/spire-${SPIRE_VERSION}/bin/spire-agent /usr/local/bin/spire-agent

# GetSecretValue is SigV4-signed, unlike AssumeRoleWithWebIdentity, so this one call
# needs a real SDK and cannot be a curl in the shell loop.
FROM golang:1.27-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS secret-fetcher

WORKDIR /src
COPY secret-fetcher/go.mod secret-fetcher/go.sum ./
RUN go mod download
COPY secret-fetcher/ ./
RUN CGO_ENABLED=0 go build -o /secret-fetcher .

FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a

# curl posts the token to STS. AssumeRoleWithWebIdentity is unsigned, so no SDK is needed.
RUN apk add --no-cache curl

COPY --from=spire /usr/local/bin/spire-agent /usr/local/bin/spire-agent
COPY --from=secret-fetcher /secret-fetcher /usr/local/bin/secret-fetcher

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

RUN addgroup -S app && adduser -S app -G app
# Numeric, not the name - the kubelet cannot verify runAsNonRoot for a user it
# cannot resolve, and fails the container closed. Same uid the account already has.
USER 100

ENTRYPOINT ["/entrypoint.sh"]
