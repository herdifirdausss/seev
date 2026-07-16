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

USER nonroot:nonroot
EXPOSE 8080 8081 8082 8083 8090 8091 8092 8093 8094 9091 9092 9093 9094

ENTRYPOINT ["/app/service"]
