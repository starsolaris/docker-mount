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

- **Linux ≥ 5.8** (for `AT_RECURSIVE` in `open_tree`)
- **Root** or `CAP_SYS_ADMIN` + `CAP_SYS_CHROOT`
- Docker (containerd/cri-o/podman planned)

## Installation

```bash
make all
sudo make install          # installs docker-mount to /usr/local/bin
sudo make install-systemd  # installs systemd unit (optional)
```

## Usage

### Daemon mode

```bash
docker-mount --target /opt/mount
```

Watches Docker events and maintains exported mounts automatically. New containers appear, removed containers are cleaned up, restarted containers get their mounts refreshed.

### CLI subcommands

| Command | Description |
|---------|-------------|
| `list` | Show all exported mounts |
| `info <name>` | Show container metadata (PID, namespace, image, etc.) |
| `cat <name> <path>` | Read a file via `/proc/<pid>/root` (no export needed) |
| `exec <name> <cmd...>` | Run a command inside a container via `docker exec` |

## Parameters

### Daemon flags

| Flag | Default | Description |
|------|---------|-------------|
| `--target` | `/opt/mount` | Directory where container filesystems are exported. Each container gets a subdirectory: `<target>/<container-name>` |
| `--helper` | *(embedded)* | Path to the C helper binary. Empty by default — the helper is embedded into the daemon binary and extracted to a temp file at runtime. Set explicitly to use an external helper |
| `--interval` | `30s` | Poll reconciliation interval. A full reconcile runs every interval to catch any events missed by Docker event streaming |
| `--cleanup-on-exit` | `true` | Recursively unmount all exports on daemon shutdown. Set `=false` to leave mounts intact |

### Signals

| Signal | Behavior |
|--------|----------|
| `SIGTERM`, `SIGINT` | Graceful shutdown. Unmounts all exports (unless `--cleanup-on-exit=false`) |

## Examples

### Start daemon

```bash
sudo docker-mount --target /opt/mount
```

### Start daemon with custom settings

```bash
sudo docker-mount \
    --target /srv/containers \
    --helper /opt/docker-mount/docker-mount-helper \
    --interval 10s \
    --cleanup-on-exit
```

### systemd

```bash
sudo make install-systemd
sudo systemctl daemon-reload
sudo systemctl enable --now docker-mount
```

### List exports

```bash
$ docker-mount list
NAME       TARGET                  MOUNT_ID
web-php    /opt/mount/web-php      1234
postgres   /opt/mount/postgres     1235
```

### Inspect a container export

```bash
$ docker-mount info web-php
Name:        web-php
PID:         12345
Namespace:   mnt:[4026536190]
Image:       php:8.2-fpm
Container:   a1b2c3d4e5f6
Target:      /opt/mount/web-php
Mounted:     yes
```

### Read a file without exporting

```bash
$ docker-mount cat web-php /etc/php/8.2/fpm/php-fpm.conf
```

### Run a command in a container

```bash
$ docker-mount exec web-php php -v
PHP 8.2.18 (cli)
```

### Work with exported filesystem

```bash
$ vim /opt/mount/web-php/var/www/html/index.php
$ grep -r "PDO" /opt/mount/web-php/var/www/
$ rsync -a /opt/mount/web-php/var/www/ /backup/web-php/
```

## Behavior

### Container restart

Overlay survives — mount continues working. Next reconcile detects namespace change and replaces the mount.

### Container removal

Mount is automatically cleaned up (`umount -R`).

### Container re-creation (same name)

New overlay → new superblock. Old mount is replaced with the new one.

### Read-only volumes

Bind mounts marked `:ro` inside the container remain read-only in the export.

### PID reuse

Namespace inode comparison prevents false matches when PIDs are reused.

## Building from source

```bash
make all       # builds docker-mount (helper embedded)
make helper    # C helper only (needs gcc or musl-gcc)
make build     # Go daemon only (needs Go ≥ 1.21 + embedded helper)
make vet       # run go vet
make test      # run tests
make clean     # remove build artifacts
```

### Note on helper binary size

`docker-mount-helper` is ~700K when built with glibc. This is normal — glibc's
static `printf` pulls in locale and NSS infrastructure that can't be removed.
The code itself is 153 lines.

For a minimal binary (~15K), install musl:

```bash
# Debian/Ubuntu
apt install musl-tools
# Fedora/RHEL
dnf install musl-gcc

make helper  # picks up musl-gcc automatically
```

## License

MIT
