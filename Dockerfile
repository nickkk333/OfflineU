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

# Version is injected at build time: a git tag becomes X.Y.Z, every other build
# (CI branch/PR run, fnos/build.ps1, build-windows.ps1, docker/run.ps1) uses
# latest - the same rule everywhere, so the version is never typed by hand.
ARG VERSION=latest

COPY go.mod ./
COPY main.go ./
COPY internal/ ./internal/
# //go:embed all:web/dist needs the bundle at compile time; it comes straight
# from the frontend stage, so it never has to be committed to the repository.
COPY --from=frontend /web/dist ./web/dist

# CGO_ENABLED=0 produces a static binary that runs on plain Alpine.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/nickkk333/offlineu/internal/offlineu.Version=${VERSION}" -o /out/offlineu .

# ---------------------------------------------------------------------------
# Stage 3 - minimal runtime image
# ---------------------------------------------------------------------------
FROM alpine:3.21

# libcap provides setcap/getcap: setcap applies the read capability below at build
# time, and getcap stays in the image so the state can be inspected later with
# "docker exec <container> getcap /app/offlineu-cap".
#
# ffmpeg/ffprobe are bundled unconditionally: the server repackages lessons a
# browser or a renderer would otherwise refuse (an .mkv or MPEG-TS becomes a
# browser-native MP4 on the fly), so a viewer that stops at "unsupported format"
# is worse than a ~90 MB bigger image. Without it OfflineU can only hand the
# original file to the client, which then fails to play it.
RUN apk add --no-cache ca-certificates libcap ffmpeg \
    && adduser -D -u 10001 -h /app offlineu

WORKDIR /app
COPY --from=build /out/offlineu /app/offlineu
COPY docker/entrypoint.sh /entrypoint.sh

# Defaults for container use:
#  - /courses is the mount point: map your own folder onto it
#    (-v "/host/path/to/courses:/courses:ro"). OfflineU refuses to guess, and the
#    UI asks for the mapping when the folder is still empty.
#  - progress and the course list go to /app/data so course mounts stay read-only
ENV OFFLINEU_ROOTS=/courses \
    OFFLINEU_PROGRESS_DIR=/app/data \
    OFFLINEU_HOST=0.0.0.0 \
    OFFLINEU_PORT=5000

RUN mkdir -p /courses /app/data && chown -R offlineu:offlineu /app /courses

# The same binary is installed twice:
#   /app/offlineu      plain
#   /app/offlineu-cap  identical, plus the file capability CAP_DAC_OVERRIDE
# That capability lets the unprivileged user 10001 read a mapped course folder
# which belongs to root or to another NAS account, so neither a chmod on the NAS
# nor ticking "使用高权限执行容器" (--privileged) is needed - CAP_DAC_OVERRIDE is part
# of Docker's default capability set. /entrypoint.sh starts that copy and falls
# back to the plain one when the container dropped the capability
# (--cap-drop DAC_OVERRIDE, --cap-drop ALL, no-new-privileges), because executing
# a file whose capability is not in the capability bounding set fails with EPERM.
#
# setcap has to run AFTER chown - changing a file's owner clears its file
# capabilities again (it did, back when chown was the last step). The test keeps
# a future edit from silently removing the capability.
RUN cp /app/offlineu /app/offlineu-cap \
    && chown offlineu:offlineu /app/offlineu-cap \
    && setcap cap_dac_override+ep /app/offlineu-cap \
    && [ "$(getcap /app/offlineu-cap | awk '{print $2}')" = "cap_dac_override=ep" ] \
    && sed -i 's/\r$//' /entrypoint.sh \
    && chmod 755 /entrypoint.sh

# Declared as volumes so container GUIs (飞牛OS / fnOS, Synology, Unraid, Docker
# Desktop, …) list both paths as folders you have to map while creating the
# container:
#   /courses   your course library (mount it read-only)
#   /app/data  progress + course list (must stay writable)
# Declaring them after the chown above lets the mapped folder inherit the offlineu
# ownership instead of turning up root-owned.
VOLUME ["/courses", "/app/data"]

LABEL org.opencontainers.image.title="OfflineU" \
      org.opencontainers.image.description="Self-hosted offline course viewer. Map your course folder to /courses (read-only) and keep /app/data writable for progress."

# The server runs as the unprivileged user offlineu (uid 10001). A mapped folder
# that only root (or another NAS account) may open needs no extra setting: the
# copy with CAP_DAC_OVERRIDE takes care of it, so neither "使用高权限执行容器"
# (--privileged) nor a chmod on the NAS is required. A container that runs the
# plain copy (because the capability was dropped) is reported by the picker; see
# "Permissions: which user reads your folders" in README.md for the fallbacks
# (chmod, user: "1000:1000", --user 0:0). The user can only be chosen while the
# container is created - nothing inside it can gain root later.
USER offlineu
EXPOSE 5000

# The base image ships BusyBox wget, so no extra package is needed.
HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:5000/health >/dev/null 2>&1 || exit 1

# Optional course path can be appended: docker run ... /courses/My Course
# The entrypoint picks the copy with the read capability (CAP_DAC_OVERRIDE) and
# keeps the container bootable when that is impossible.
ENTRYPOINT ["/entrypoint.sh"]
