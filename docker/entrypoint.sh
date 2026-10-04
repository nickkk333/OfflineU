#!/bin/sh
# OfflineU's container entrypoint.
#
# The image ships the same binary twice:
#
#   /app/offlineu      plain, without file capabilities
#   /app/offlineu-cap  identical, plus the CAP_DAC_OVERRIDE file capability
#
# CAP_DAC_OVERRIDE lets the unprivileged user (uid 10001) read a mapped course
# folder that belongs to root or to another NAS account. Nothing has to be
# chmod-ed on the NAS, and the "run with high privileges" checkbox of the fnOS
# container dialog (--privileged) does not have to be ticked - it would not help
# on its own either: privilege belongs to the user a container runs as, and the
# Linux kernel drops the capability sets when a non-root process executes a file
# that carries no file capabilities at all.
#
# The capability only works while CAP_DAC_OVERRIDE is part of the container's
# capability bounding set. When it is not (--cap-drop DAC_OVERRIDE, --cap-drop
# ALL or no-new-privileges) execve refuses /app/offlineu-cap with EPERM, which
# would kill the container before OfflineU starts at all. The probe below detects
# exactly that and falls back to the plain copy: the picker then reports the
# folder as unreadable (with the usual fixes) instead of refusing to boot.
BINARY=/app/offlineu
if [ -x /app/offlineu-cap ] && /app/offlineu-cap --cap-check >/dev/null 2>&1; then
    BINARY=/app/offlineu-cap
fi
exec "$BINARY" "$@"