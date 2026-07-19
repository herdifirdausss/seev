# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN set -eu; \
    for service in gateway auth-service ledger-service payin-service payout-service fraud-service; do \
        CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o "/out/${service}" "./cmd/${service}"; \
    done

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

ARG SERVICE=gateway
COPY --from=builder /out/${SERVICE} /app/service
COPY --from=builder /src/migrations /app/migrations

# docs/plan/44 K5 — CI's Bake build passes the commit SHA as REVISION so a
# smoke-container run can assert every one of the six loaded images was
# actually built from the commit under test, not a stale cache hit or a
# leftover local `:dev` tag from an earlier run.
ARG REVISION=unknown
LABEL org.opencontainers.image.revision=${REVISION}

USER nonroot:nonroot
EXPOSE 8080 8081 8082 8083 8090 8091 8092 8093 8094 9091 9092 9093 9094

ENTRYPOINT ["/app/service"]
