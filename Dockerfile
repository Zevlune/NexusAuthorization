FROM golang:1.25-alpine AS api-build

WORKDIR /src
COPY src/go.mod src/go.sum ./
RUN go mod download
COPY src/cmd ./cmd
COPY src/internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/nexus-authorization ./cmd/api-server

FROM alpine:3.22 AS api

RUN apk add --no-cache ca-certificates wget su-exec \
    && addgroup -S nexus-authorization \
    && adduser -S -H -G nexus-authorization -h /app nexus-authorization \
    && mkdir -p /app/data \
    && chown nexus-authorization:nexus-authorization /app/data \
    && chmod 700 /app/data
WORKDIR /app
COPY --from=api-build /out/nexus-authorization /app/nexus-authorization
COPY src/configs/app.production.yaml /app/configs/app.yaml
# The entrypoint fixes /app/data ownership, then drops to the nexus-authorization user.
# Strip CR in case the script was checked out with CRLF line endings.
COPY docker/nexus-authorization-entrypoint.sh /usr/local/bin/nexus-authorization-entrypoint.sh
RUN sed -i 's/\r$//' /usr/local/bin/nexus-authorization-entrypoint.sh \
    && chmod 755 /usr/local/bin/nexus-authorization-entrypoint.sh
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=6 \
    CMD wget --quiet --spider http://127.0.0.1:8080/api/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/nexus-authorization-entrypoint.sh"]
