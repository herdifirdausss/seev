# syntax=docker/dockerfile:1@sha256:87999aa3d42bdc6bea60565083ee17e86d1f3339802f543c0d03998580f9cb89

# Manifest-list digest verified 2026-08-04; keep the version tag for human
# readability and the digest for reproducible resolution.
FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN set -eu; \
    for service in gateway auth-service ledger-service payin-service payout-service fraud-service vendor-service admin-bff-service assurance-service mock-push-provider; do \
        case "$service" in \
            gateway) path=./services/gateway/cmd/gateway;; \
            auth-service) path=./services/auth/cmd/auth;; \
            ledger-service) path=./services/ledger/cmd/ledger;; \
            payin-service) path=./services/payin/cmd/payin;; \
            payout-service) path=./services/payout/cmd/payout;; \
            fraud-service) path=./services/fraud/cmd/fraud;; \
            vendor-service) path=./services/vendor-service/cmd/vendor;; \
            admin-bff-service) path=./services/adminbff/cmd/adminbff;; \
            assurance-service) path=./services/assurance/cmd/assurance;; \
            mock-push-provider) path=./tools/mock-push-provider;; \
            *) echo "unsupported service $service" >&2; exit 2;; \
        esac; \
        CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o "/out/${service}" "$path"; \
    done

# Manifest-list digest verified 2026-08-04.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35
WORKDIR /app

ARG SERVICE=gateway
COPY --from=builder /out/${SERVICE} /app/service
COPY --from=builder /src/services/gateway/migrations /app/services/gateway/migrations
COPY --from=builder /src/services/auth/migrations /app/services/auth/migrations
COPY --from=builder /src/services/ledger/migrations /app/services/ledger/migrations
COPY --from=builder /src/services/payin/migrations /app/services/payin/migrations
COPY --from=builder /src/services/payout/migrations /app/services/payout/migrations
COPY --from=builder /src/services/fraud/migrations /app/services/fraud/migrations
COPY --from=builder /src/services/adminbff/migrations /app/services/adminbff/migrations
COPY --from=builder /src/services/assurance/migrations /app/services/assurance/migrations
COPY --from=builder /src/services/vendor-service/migrations /app/services/vendor-service/migrations

# docs/roadmap/archive/44 K5 — CI's Bake build passes the commit SHA as REVISION so a
# smoke-container run can assert every loaded application image was
# actually built from the commit under test, not a stale cache hit or a
# leftover local `:dev` tag from an earlier run.
ARG REVISION=unknown
LABEL org.opencontainers.image.revision=${REVISION}

USER nonroot:nonroot
EXPOSE 8080 8081 8082 8083 8090 8091 8092 8093 8094 8095 8096 8097 8098 9091 9092 9093 9094 9098

ENTRYPOINT ["/app/service"]
