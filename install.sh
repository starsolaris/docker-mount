#!/bin/sh
set -eu

# docker-mount installer — pipe via curl:
#   curl -fsSL https://raw.githubusercontent.com/starsolaris/docker-mount/main/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --systemd

REPO="starsolaris/docker-mount"
BIN="docker-mount"
PREFIX="${PREFIX:-/usr/local}"
BINDIR="${BINDIR:-$PREFIX/bin}"
VERSION="${VERSION:-latest}"
WITH_SYSTEMD=0

# --- parse flags ---
for arg in "$@"; do
    case "$arg" in
        --systemd) WITH_SYSTEMD=1 ;;
        *) err "unknown flag: $arg"; exit 1 ;;
    esac
done

# --- helpers ---
need_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "missing: $1"; exit 1; }; }
info()    { echo "  $*"; }
err()     { echo "error: $*" >&2; }

need_cmd curl
need_cmd tar
need_cmd install

# --- detect platform ---
case "$(uname -s)" in
    Linux)  OS=linux ;;
    *)      err "unsupported OS: $(uname -s)"; exit 1 ;;
esac

case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64)  ARCH=arm64 ;;
    *)      err "unsupported arch: $(uname -m)"; exit 1 ;;
esac

# --- resolve version ---
if [ "$VERSION" = "latest" ]; then
    info "resolving latest release..."
    VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
        | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
    if [ -z "$VERSION" ]; then
        err "failed to resolve latest release"
        exit 1
    fi
fi

# --- download ---
TARBALL="${BIN}-${VERSION}-${OS}-${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$VERSION/$TARBALL"

info "downloading $BIN $VERSION ($OS/$ARCH)..."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

curl --retry 3 -fsSL "$URL" -o "$tmp/$TARBALL" || {
    err "download failed: $URL"
    exit 1
}

tar -xzf "$tmp/$TARBALL" -C "$tmp" || {
    err "extract failed"
    exit 1
}

if [ ! -f "$tmp/$BIN" ]; then
    err "binary not found in tarball"
    exit 1
fi

# --- install ---
info "installing to $BINDIR/$BIN"
install -d "$BINDIR"
install -m 755 "$tmp/$BIN" "$BINDIR/$BIN"

info "$BIN $VERSION installed to $BINDIR/$BIN"

# --- systemd (optional) ---
if [ "$WITH_SYSTEMD" = "1" ]; then
    if [ "$(id -u)" != "0" ]; then
        err "--systemd requires root"
        exit 1
    fi
    need_cmd systemctl
    SERVICE="/etc/systemd/system/docker-mount.service"
    info "installing systemd unit..."
    curl --retry 3 -fsSL "https://raw.githubusercontent.com/$REPO/$VERSION/systemd/docker-mount.service" \
        -o "$tmp/docker-mount.service" || {
        err "failed to download systemd unit for $VERSION"
        exit 1
    }
    install -m 644 "$tmp/docker-mount.service" "$SERVICE"
    systemctl daemon-reload
    info "systemd unit installed. enable with: systemctl enable --now docker-mount"
fi

info "done. run: sudo $BINDIR/$BIN --target /opt/mount"
