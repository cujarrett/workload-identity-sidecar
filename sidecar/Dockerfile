FROM alpine:3.24 AS spire

ARG SPIRE_VERSION=1.15.3
ARG TARGETARCH
RUN apk add --no-cache curl tar && \
    curl -sSL "https://github.com/spiffe/spire/releases/download/v${SPIRE_VERSION}/spire-${SPIRE_VERSION}-linux-${TARGETARCH}-musl.tar.gz" \
      -o /tmp/spire.tar.gz && \
    tar -xzf /tmp/spire.tar.gz -C /tmp && \
    mv /tmp/spire-${SPIRE_VERSION}/bin/spire-agent /usr/local/bin/spire-agent

FROM alpine:3.24

# curl posts the token to STS. AssumeRoleWithWebIdentity is unsigned, so no SDK is needed.
RUN apk add --no-cache curl

COPY --from=spire /usr/local/bin/spire-agent /usr/local/bin/spire-agent

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

RUN addgroup -S app && adduser -S app -G app
# Numeric, not the name - the kubelet cannot verify runAsNonRoot for a user it
# cannot resolve, and fails the container closed. Same uid the account already has.
USER 100

ENTRYPOINT ["/entrypoint.sh"]
