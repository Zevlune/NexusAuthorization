FROM golang:1.25-alpine AS api-build

WORKDIR /src
COPY src/go.mod src/go.sum ./
RUN go mod download
COPY src/cmd ./cmd
COPY src/internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/permission-api ./cmd/api-server

FROM alpine:3.22 AS api

RUN apk add --no-cache ca-certificates wget \
    && addgroup -S nexus-authorization \
    && adduser -S -G nexus-authorization nexus-authorization \
    && mkdir -p /app/data \
    && chown nexus-authorization:nexus-authorization /app/data \
    && chmod 700 /app/data
WORKDIR /app
COPY --from=api-build /out/permission-api /app/permission-api
COPY src/configs/app.production.yaml /app/configs/app.yaml
USER nexus-authorization
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=5s --start-period=10s --retries=6 \
    CMD wget --quiet --spider http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app/permission-api"]
