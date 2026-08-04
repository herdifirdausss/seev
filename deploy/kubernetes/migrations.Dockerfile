# syntax=docker/dockerfile:1@sha256:87999aa3d42bdc6bea60565083ee17e86d1f3339802f543c0d03998580f9cb89
# Explicit migration image for the Kubernetes pre-install/pre-upgrade Job.
# It deliberately contains only the migration binary and SQL files.
# Manifest-list digest verified 2026-08-04.
FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./tools/migrate

# Manifest-list digest verified 2026-08-04.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35
WORKDIR /app
COPY --from=builder /out/migrate /app/migrate
COPY --from=builder /src/services /app/services
USER nonroot:nonroot
ENTRYPOINT ["/app/migrate"]
