package ns

import (
	"os"
	"testing"
)

func TestSelf(t *testing.T) {
	ns, err := Self()
	if err != nil {
		t.Fatalf("Self(): %v", err)
	}
	defer ns.Close()

	if ns.Inode == 0 {
		t.Error("Self(): Inode is 0, expected non-zero")
	}
	if ns.Path != "/proc/self/ns/mnt" {
		t.Errorf("Self(): Path = %q, want /proc/self/ns/mnt", ns.Path)
	}
}

func TestOpenCurrentPID(t *testing.T) {
	ns, err := Open(os.Getpid())
	if err != nil {
		t.Fatalf("Open(%d): %v", os.Getpid(), err)
	}
	defer ns.Close()

	if ns.Inode == 0 {
		t.Error("Inode is 0")
	}
}

func TestSelfEqual(t *testing.T) {
	a, err := Self()
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()

	b, err := Self()
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if !a.Equal(b) {
		t.Error("two Self() should be Equal")
	}
	if a.Inode != b.Inode {
		t.Errorf("inodes differ: %d vs %d", a.Inode, b.Inode)
	}
}

func TestOpenAndSelfEqual(t *testing.T) {
	ns, err := Open(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	defer ns.Close()

	self, err := Self()
	if err != nil {
		t.Fatal(err)
	}
	defer self.Close()

	if !ns.Equal(self) {
		t.Error("Open(pid) should Equal Self() for own PID")
	}
}

func TestNilEqual(t *testing.T) {
	var a, b *MountNS
	if !a.Equal(b) {
		t.Error("nil Equal nil should be true")
	}

	ns, err := Self()
	if err != nil {
		t.Fatal(err)
	}
	defer ns.Close()

	if ns.Equal(nil) {
		t.Error("non-nil Equal nil should be false")
	}
	if a.Equal(ns) {
		t.Error("nil Equal non-nil should be false")
	}
}

func TestOpenInvalidPID(t *testing.T) {
	_, err := Open(99999999)
	if err == nil {
		t.Error("Open(invalid PID) should return error")
	}
}

func TestClose(t *testing.T) {
	ns, err := Self()
	if err != nil {
		t.Fatal(err)
	}
	if err := ns.Close(); err != nil {
		t.Errorf("Close(): %v", err)
	}
	if err := ns.Close(); err == nil {
		t.Error("second Close() should return error (fd already closed)")
	}
}
