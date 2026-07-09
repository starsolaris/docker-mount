#define _GNU_SOURCE
#include <errno.h>
#include <fcntl.h>
#include <sched.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mount.h>
#include <sys/stat.h>
#include <sys/syscall.h>
#include <unistd.h>
/* Constants not guaranteed in all kernel headers */
#ifndef OPEN_TREE_CLONE
#define OPEN_TREE_CLONE  1
#endif
#ifndef OPEN_TREE_CLOEXEC
#define OPEN_TREE_CLOEXEC  O_CLOEXEC
#endif
#ifndef AT_RECURSIVE
#define AT_RECURSIVE      0x8000
#endif
#ifndef AT_EMPTY_PATH
#define AT_EMPTY_PATH     0x1000
#endif
#ifndef MOVE_MOUNT_F_EMPTY_PATH
#define MOVE_MOUNT_F_EMPTY_PATH  0x00000004
#endif
#ifndef MOVE_MOUNT_F_REPLACE
#define MOVE_MOUNT_F_REPLACE     0x00000008
#endif

#ifndef MOUNT_ATTR_NOSUID
#define MOUNT_ATTR_NOSUID  0x00000002
#endif
#ifndef MOUNT_ATTR_NODEV
#define MOUNT_ATTR_NODEV   0x00000004
#endif
#ifndef MS_PRIVATE
#define MS_PRIVATE  (1 << 18)
#endif

/* struct mount_attr — guard against systems that already define it */
#ifndef MOUNT_ATTR_SIZE_VER0
#define MOUNT_ATTR_SIZE_VER0  32
struct mount_attr {
	uint64_t attr_set;
	uint64_t attr_clr;
	uint64_t propagation;
	uint64_t userns_fd;
};
#endif

/* Syscall wrappers */
static inline int sys_open_tree(int dfd, const char *path, unsigned int flags)
{
	return (int)syscall(SYS_open_tree, dfd, path, flags);
}

static inline int sys_move_mount(int from_dfd, const char *from_path,
				  int to_dfd, const char *to_path,
				  unsigned int flags)
{
	return (int)syscall(SYS_move_mount, from_dfd, from_path,
			    to_dfd, to_path, flags);
}

static inline int sys_mount_setattr(int dfd, const char *path,
				     unsigned int flags,
				     struct mount_attr *attr, size_t size)
{
	return (int)syscall(SYS_mount_setattr, dfd, path, flags, attr, size);
}

static void die(const char *msg)
{
	fprintf(stderr, "mount-helper: %s: %s\n", msg, strerror(errno));
	exit(1);
}

int main(int argc, char *argv[])
{
	const char *usage = "Usage: docker-mount-helper <pid> <target>\n";
	int host_fd, container_fd, tree_fd;

	if (argc != 3)
		die(usage);

	const char *pid_str  = argv[1];
	const char *target   = argv[2];

	/* 1. Open host mount namespace (save it for return) */
	host_fd = open("/proc/self/ns/mnt", O_RDONLY);
	if (host_fd < 0)
		die("open /proc/self/ns/mnt");

	/* 2. Open container mount namespace */
	char ns_path[64];
	int n = snprintf(ns_path, sizeof(ns_path), "/proc/%s/ns/mnt", pid_str);
	if (n < 0 || (size_t)n >= sizeof(ns_path))
		die("snprintf ns_path");
	container_fd = open(ns_path, O_RDONLY);
	if (container_fd < 0)
		die("open container ns");

	/* 3. Enter container mount namespace */
	if (setns(container_fd, 0) < 0)
		die("setns container");
	close(container_fd);

	/* 4. Clone the mount tree recursively */
	tree_fd = sys_open_tree(AT_FDCWD, "/",
				OPEN_TREE_CLONE | OPEN_TREE_CLOEXEC | AT_RECURSIVE);
	if (tree_fd < 0)
		die("open_tree");

	/* 5. Return to host mount namespace */
	if (setns(host_fd, 0) < 0)
		die("setns host");
	close(host_fd);

	/* 6. Ensure target directory exists */
	if (mkdir(target, 0755) < 0 && errno != EEXIST)
		die("mkdir target");

	/* 7. Move the cloned mount tree to target */
	int ret = sys_move_mount(tree_fd, "", AT_FDCWD, target,
				MOVE_MOUNT_F_EMPTY_PATH | MOVE_MOUNT_F_REPLACE);
	if (ret < 0 && errno == EINVAL) {
		/* Fallback for kernels lacking MOVE_MOUNT_F_REPLACE (< 6.8) */
		if (umount2(target, MNT_DETACH) < 0 && errno != EINVAL && errno != ENOENT)
			die("umount2 detach");
		ret = sys_move_mount(tree_fd, "", AT_FDCWD, target,
				    MOVE_MOUNT_F_EMPTY_PATH);
	}
	if (ret < 0)
		die("move_mount");
	close(tree_fd);

	/* 8. Set mount attributes (best-effort, Linux 5.12+) */
	struct mount_attr attr = {
		.attr_set    = MOUNT_ATTR_NOSUID | MOUNT_ATTR_NODEV,
		.attr_clr    = 0,
		.propagation = MS_PRIVATE,
		.userns_fd   = 0,
	};
	(void)sys_mount_setattr(AT_FDCWD, target, AT_RECURSIVE,
				&attr, sizeof(attr));

	printf("ok\n");
	return 0;
}
