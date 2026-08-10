#!/bin/sh
# Named volumes are root-owned; the app runs as uid 1000. Fix ownership once
# at start (when we are still root), then drop privileges.
set -eu

DATA_DIR="${IMAGE_SEARCH_DATA_DIR:-/var/lib/image-search}"
mkdir -p "$DATA_DIR"

if [ "$(id -u)" = "0" ]; then
	chown -R imagesearch:imagesearch "$DATA_DIR"
	exec gosu imagesearch "$@"
fi

exec "$@"
