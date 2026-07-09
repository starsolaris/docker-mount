// Package ns provides low-level Linux namespace file descriptor operations.
package ns

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// MountNS represents an open mount namespace file descriptor.
type MountNS struct {
	file  *os.File // holds reference so GC does not close the fd
	Inode uint64   // namespace inode number from fstat
	Path  string   // original path for debugging
}

// FD returns the underlying file descriptor.
func (ns *MountNS) FD() int {
	return int(ns.file.Fd())
}

// Open opens /proc/<pid>/ns/mnt, performs fstat to capture the inode,
// and returns a MountNS. The caller must call Close() when done.
//
// The approach of opening the fd first and then calling fstat avoids
// the race with PID reuse — if the process exits between readlink and
// open, open returns ENOENT instead of silently reading a reused PID.
func Open(pid int) (*MountNS, error) {
	path := fmt.Sprintf("/proc/%d/ns/mnt", pid)
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	var stat unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &stat); err != nil {
		f.Close()
		return nil, fmt.Errorf("fstat %s: %w", path, err)
	}

	return &MountNS{
		file:  f,
		Inode: stat.Ino,
		Path:  path,
	}, nil
}

// Close closes the underlying namespace file descriptor.
func (ns *MountNS) Close() error {
	return ns.file.Close()
}

// Equal returns true if two MountNS refer to the same namespace (same inode).
func (ns *MountNS) Equal(other *MountNS) bool {
	if ns == nil || other == nil {
		return ns == other
	}
	return ns.Inode == other.Inode
}

// Self returns the host's own mount namespace.
func Self() (*MountNS, error) {
	return OpenByPath("/proc/self/ns/mnt")
}

// OpenByPath opens an arbitrary namespace path.
func OpenByPath(path string) (*MountNS, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	var stat unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &stat); err != nil {
		f.Close()
		return nil, fmt.Errorf("fstat %s: %w", path, err)
	}

	return &MountNS{
		file:  f,
		Inode: stat.Ino,
		Path:  path,
	}, nil
}
