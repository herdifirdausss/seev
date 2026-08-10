# syntax=docker/dockerfile:1
# Builds either the Wallet or Operator Next.js application.
#
# Build context must be the web/ directory (not the repo root):
#   docker build --build-arg APP=wallet   -t seev/wallet   web/
#   docker build --build-arg APP=operator -t seev/operator web/
#
# Via Compose:
#   docker compose --profile frontend build
#   docker compose --profile frontend up wallet
#   docker compose --profile frontend up operator

ARG NODE_VERSION=22.17.1
ARG PNPM_VERSION=11.21.0

# ── Base: pinned Node + pnpm ──────────────────────────────────────────────────
FROM node:${NODE_VERSION}-alpine AS base
ARG PNPM_VERSION
RUN corepack enable && corepack prepare pnpm@${PNPM_VERSION} --activate
WORKDIR /app

# ── Install all dependencies ──────────────────────────────────────────────────
# Copy workspace manifests first so this layer is cached across source changes.
FROM base AS deps
COPY package.json pnpm-workspace.yaml pnpm-lock.yaml ./
COPY packages/config/package.json     packages/config/
COPY packages/domain/package.json     packages/domain/
COPY packages/money/package.json      packages/money/
COPY packages/api-client/package.json packages/api-client/
COPY packages/ui/package.json         packages/ui/
COPY packages/testing/package.json    packages/testing/
COPY apps/wallet/package.json         apps/wallet/
COPY apps/operator/package.json       apps/operator/
RUN pnpm install --frozen-lockfile

# ── Build the target application ──────────────────────────────────────────────
FROM base AS builder
ARG APP=wallet
ENV NEXT_TELEMETRY_DISABLED=1
COPY --from=deps /app/node_modules ./node_modules
COPY . .
RUN pnpm --filter "@seev/${APP}" build

# ── Production runtime ────────────────────────────────────────────────────────
FROM base AS runner
ARG APP=wallet
ARG PORT=3000
ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
ENV PORT=${PORT}
# Bake APP into the image so the CMD can reference it at runtime.
ENV APP=${APP}

# Minimal workspace manifests for pnpm to resolve the start script.
COPY package.json pnpm-workspace.yaml ./
COPY apps/${APP}/package.json apps/${APP}/

# node_modules — includes the next binary and all runtime dependencies.
COPY --from=deps /app/node_modules ./node_modules

# Built Next.js output and static assets.
COPY --from=builder /app/apps/${APP}/.next  apps/${APP}/.next
COPY --from=builder /app/apps/${APP}/public apps/${APP}/public

EXPOSE ${PORT}

# Shell form so $APP expands from ENV at container start.
CMD pnpm --filter "@seev/$APP" start
