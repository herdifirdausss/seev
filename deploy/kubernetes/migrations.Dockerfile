# Explicit migration image for the Kubernetes pre-install/pre-upgrade Job.
# It deliberately contains only the migration binary and SQL files.
FROM golang:1.26.5-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=builder /out/migrate /app/migrate
COPY --from=builder /src/migrations /app/migrations
USER nonroot:nonroot
ENTRYPOINT ["/app/migrate"]
