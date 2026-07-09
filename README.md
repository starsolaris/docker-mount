# docker-mount

Export container filesystems as regular directories on the host.

```
/opt/mount/web-php
/opt/mount/postgres
/opt/mount/nginx
```

These directories behave like normal filesystems — `vim`, `grep`, `find`, `rsync`, IDEs, and any POSIX tool work natively. No FUSE. No `nsenter`.

## How it works

Uses Linux mount API (`setns` + `open_tree` + `move_mount`) to clone a container's mount namespace and attach it to the host filesystem. The exported mount shares superblock and inodes with the container — changes are instantly visible both ways.

```
setns(container) → open_tree(CLONE|RECURSIVE) → setns(host) → move_mount → mount_setattr
```

## Requirements

- Linux ≥ 5.8
- Root or `CAP_SYS_ADMIN` + `CAP_SYS_CHROOT`
- Docker

## Installation

```bash
curl -fsSL https://raw.githubusercontent.com/starsolaris/docker-mount/main/install.sh | sh
```

With systemd:

```bash
curl -fsSL https://raw.githubusercontent.com/starsolaris/docker-mount/main/install.sh | sh -s -- --systemd
```

### From source

```bash
make all
sudo make install
```

## Usage

### Daemon

```bash
sudo docker-mount --target /opt/mount
```

Watches Docker events and maintains exported mounts automatically. New containers appear, removed containers are cleaned up, restarted containers get their mounts refreshed.

### CLI

| Command | Description |
|---------|-------------|
| `list` | Show all exported mounts |
| `info <name>` | Container metadata: PID, namespace, image |
| `cat <name> <path>` | Read a file via `/proc/<pid>/root` — no `--target` needed |
| `exec <name> <cmd...>` | Run command inside container — no `--target` needed |

```
$ docker-mount --target /opt/mount list
NAME       TARGET                  MOUNT_ID
web-php    /opt/mount/web-php      1234
postgres   /opt/mount/postgres     1235

$ docker-mount --target /opt/mount info web-php
Name:        web-php
PID:         12345
Namespace:   mnt:[4026536190]
Image:       php:8.2-fpm
Container:   a1b2c3d4e5f6
Target:      /opt/mount/web-php
Mounted:     yes
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--target` | *(required)* | Directory where container filesystems are exported |
| `--helper` | *(embedded)* | Path to external C helper. Default: extracted from binary at runtime |
| `--interval` | `30s` | Poll reconciliation fallback — catches missed Docker events after daemon restart or connection loss |
| `--cleanup-on-exit` | `true` | Unmount all exports on shutdown |

## Examples

```bash
# daemon
sudo docker-mount --target /opt/mount

# custom settings
sudo docker-mount --target /srv/fs --interval 10s --cleanup-on-exit=false

# systemd
sudo make install-systemd
sudo systemctl enable --now docker-mount

# list exports
docker-mount --target /opt/mount list

# inspect
docker-mount --target /opt/mount info web-php

# read a file (no --target needed)
docker-mount cat web-php /etc/hostname

# run a command (no --target needed)
docker-mount exec web-php php -v

# work with exported filesystem
vim /opt/mount/web-php/var/www/html/index.php
grep -r "PDO" /opt/mount/web-php/var/www/
rsync -a /opt/mount/web-php/var/www/ /backup/web-php/
```

### Systemd

```bash
# via installer
curl -fsSL .../install.sh | sh -s -- --systemd

# or from source
sudo make install-systemd
sudo systemctl enable --now docker-mount
```

The unit expects `/usr/local/bin/docker-mount`. Edit `ExecStart` in `/etc/systemd/system/docker-mount.service` to set `--target`.

## Building from source

```bash
make all       # build docker-mount (helper embedded)
make build     # Go daemon only (needs Go ≥ 1.21)
make vet       # run go vet
make test      # run tests
make clean     # remove build artifacts
```

### Note on helper binary size

The C helper is ~700K when built with glibc (glibc's static `printf` pulls in locale and NSS). With musl it's ~30K. The Makefile auto-detects `musl-gcc` and falls back to `gcc`:

```bash
# Debian/Ubuntu
apt install musl-tools
# Fedora/RHEL
dnf install musl-gcc

make all  # picks up musl-gcc automatically
```

The helper is embedded into the daemon binary — the standalone size only matters for the build step.

## Behavior

### Container restart

Overlay survives — mount continues working. Next reconcile detects namespace change and replaces the mount.

### Container removal

Mount is automatically cleaned up (`umount -R`).

### Container re-creation (same name)

New overlay → new superblock. Old mount is replaced atomically
(Linux 6.8+) or via lazy umount + remount (older kernels).

### Read-only volumes

Bind mounts marked `:ro` inside the container remain read-only in the export.

### PID reuse

Namespace inode comparison prevents false matches when PIDs are reused.

### Daemon restart

On startup, orphan mounts (containers no longer running) are cleaned up. Active container mounts are left untouched — no unnecessary remounts.

### Signals

| Signal | Behavior |
|--------|----------|
| `SIGTERM`, `SIGINT` | Graceful shutdown. Unmounts all exports (unless `--cleanup-on-exit=false`) |

## License

MIT
