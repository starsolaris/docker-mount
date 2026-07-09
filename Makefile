.PHONY: all build helper clean install test vet

PREFIX    ?= /usr/local
BINDIR    ?= $(PREFIX)/bin

HELPER_SRC = helper/main.c
HELPER_BIN = docker-mount-helper
EMBED_HELPER = cmd/docker-mount/docker-mount-helper
DAEMON_DIR = ./cmd/docker-mount
DAEMON_BIN = docker-mount

# Prefer musl for small static binaries (~15K vs ~700K with glibc).
# Falls back to gcc if musl-gcc is not installed.
CC        := $(shell command -v musl-gcc 2>/dev/null || echo gcc)
CFLAGS    = -static -O2 -Wall
LDFLAGS   = -s
GOFLAGS   = -trimpath -ldflags="-s -w"

all: helper $(EMBED_HELPER) build

helper: $(HELPER_SRC)
	$(CC) $(CFLAGS) $(LDFLAGS) -o $(HELPER_BIN) $(HELPER_SRC)

$(EMBED_HELPER): helper
	cp $(HELPER_BIN) $(EMBED_HELPER)

build: $(EMBED_HELPER)
	go build $(GOFLAGS) -o $(DAEMON_BIN) $(DAEMON_DIR)

test: $(EMBED_HELPER)
	go test ./...

vet: $(EMBED_HELPER)
	go vet ./...

install: build
	install -d $(DESTDIR)$(BINDIR)
	install -m 755 $(DAEMON_BIN) $(DESTDIR)$(BINDIR)/$(DAEMON_BIN)

install-systemd:
	install -d $(DESTDIR)/etc/systemd/system
	install -m 644 systemd/docker-mount.service $(DESTDIR)/etc/systemd/system/docker-mount.service

clean:
	rm -f $(HELPER_BIN) $(DAEMON_BIN) $(EMBED_HELPER)
