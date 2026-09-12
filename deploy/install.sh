#!/bin/sh
# DESTDIR stages files without creating accounts or contacting systemd.
set -eu
binary=${1:-bin/indihurd}
stage=${DESTDIR:-}
source_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
[ -f "$binary" ] || { echo "Build first: make build" >&2; exit 1; }
if [ -z "$stage" ]; then
    [ "$(id -u)" -eq 0 ] || { echo "Run installation as root (sudo make install)." >&2; exit 1; }
    getent group indihurd >/dev/null || groupadd --system indihurd
    if ! id indihurd >/dev/null 2>&1; then
        useradd --system --gid indihurd --home-dir /var/lib/indihurd --no-create-home --shell /usr/sbin/nologin indihurd
    fi
    for group in dialout video plugdev; do
        if getent group "$group" >/dev/null; then
            usermod -a -G "$group" indihurd
        fi
    done
fi
install -d "$stage/usr/local/bin" "$stage/etc/systemd/system"
install -d -m 0750 "$stage/etc/indihurd" "$stage/var/lib/indihurd"
install -m 0755 "$binary" "$stage/usr/local/bin/indihurd"
install -m 0644 "$source_dir/indihurd.service" "$stage/etc/systemd/system/indihurd.service"
if [ ! -e "$stage/etc/indihurd/indihurd.conf" ]; then
    install -m 0640 "$source_dir/indihurd.conf" "$stage/etc/indihurd/indihurd.conf"
fi
if [ -z "$stage" ]; then
    chown indihurd:indihurd /etc/indihurd /etc/indihurd/indihurd.conf /var/lib/indihurd
    if [ -d /run/systemd/system ]; then
        systemctl daemon-reload
    fi
    echo "Installed. Start at boot and now: sudo systemctl enable --now indihurd"
    echo "For an already running installation: sudo systemctl restart indihurd"
fi
