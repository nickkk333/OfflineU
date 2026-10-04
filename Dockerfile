# OfflineU runs as a single static Go binary with the Vue frontend embedded in
# it, so the runtime image contains neither Python nor Node.

# ---------------------------------------------------------------------------
# Stage 1 - build the Vue 3 frontend (Vite) into plain static assets
# ---------------------------------------------------------------------------
FROM node:22-alpine AS frontend

WORKDIR /web

# Dependencies are copied first so this layer stays cached while only the
# sources change.
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY web/ ./
RUN npm run build

# ---------------------------------------------------------------------------
# Stage 2 - compile the Go binary with the bytes of web/dist embedded
# ---------------------------------------------------------------------------
FROM golang:1.24-alpine AS build

WORKDIR /src

COPY go.mod ./
COPY main.go ./
COPY internal/ ./internal/
# //go:embed all:web/dist needs the bundle at compile time; it comes straight
# from the frontend stage, so it never has to be committed to the repository.
COPY --from=frontend /web/dist ./web/dist

# CGO_ENABLED=0 produces a static binary that runs on plain Alpine.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/offlineu .

# ---------------------------------------------------------------------------
# Stage 3 - minimal runtime image
# ---------------------------------------------------------------------------
FROM alpine:3.21

RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 -h /app offlineu

WORKDIR /app
COPY --from=build /out/offlineu /app/offlineu

# Defaults for container use:
#  - courses are mounted at /app/courses and are the only browsable/served root
#  - progress and the course list go to /app/data so course mounts stay read-only
ENV OFFLINEU_ROOTS=/app/courses \
    OFFLINEU_PROGRESS_DIR=/app/data \
    OFFLINEU_HOST=0.0.0.0 \
    OFFLINEU_PORT=5000

RUN mkdir -p /app/courses /app/data && chown -R offlineu:offlineu /app

USER offlineu
EXPOSE 5000

# The base image ships BusyBox wget, so no extra package is needed.
HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:5000/health >/dev/null 2>&1 || exit 1

# Optional course path can be appended: docker run ... /app/courses/My Course
ENTRYPOINT ["/app/offlineu"]
