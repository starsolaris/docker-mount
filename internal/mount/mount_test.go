package mount

import (
	"bufio"
	"strings"
	"testing"
)

func TestParseMountinfoLineNormal(t *testing.T) {
	line := "123 456 0:42 / /opt/mount/web-php rw,relatime - overlay overlay rw,lowerdir=...,upperdir=...,workdir=..."
	id, mp, ok := parseMountinfoLine(line)
	if !ok {
		t.Fatal("parse failed")
	}
	if id != 123 {
		t.Errorf("mountID = %d, want 123", id)
	}
	if mp != "/opt/mount/web-php" {
		t.Errorf("mountPoint = %q, want /opt/mount/web-php", mp)
	}
}

func TestParseMountinfoLineWithOptionalFields(t *testing.T) {
	line := "36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 shared:2 - ext3 /dev/root rw,errors=continue"
	id, mp, ok := parseMountinfoLine(line)
	if !ok {
		t.Fatal("parse failed")
	}
	if id != 36 {
		t.Errorf("mountID = %d, want 36", id)
	}
	if mp != "/mnt2" {
		t.Errorf("mountPoint = %q, want /mnt2", mp)
	}
}

func TestParseMountinfoLineNoOptionalFields(t *testing.T) {
	line := "1 0 0:1 / / rw - rootfs rootfs rw"
	id, mp, ok := parseMountinfoLine(line)
	if !ok {
		t.Fatal("parse failed")
	}
	if id != 1 {
		t.Errorf("mountID = %d, want 1", id)
	}
	if mp != "/" {
		t.Errorf("mountPoint = %q, want /", mp)
	}
}

func TestParseMountinfoLineProc(t *testing.T) {
	line := "124 123 0:43 / /opt/mount/web/proc rw,nosuid,nodev,noexec,relatime - proc proc rw"
	id, mp, ok := parseMountinfoLine(line)
	if !ok {
		t.Fatal("parse failed")
	}
	if id != 124 {
		t.Errorf("mountID = %d, want 124", id)
	}
	if mp != "/opt/mount/web/proc" {
		t.Errorf("mountPoint = %q, want /opt/mount/web/proc", mp)
	}
}

func TestParseMountinfoLineMalformed(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{"empty", ""},
		{"too short", "1 2 3 4 5"},
		{"no separator", "1 2 3:4 / /mnt rw ext3 /dev/sda rw"},
		{"separator too early", "- 2 3:4 root /mnt rw ext3 /dev/sda rw"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ok := parseMountinfoLine(tt.line)
			if ok {
				t.Error("expected parse failure")
			}
		})
	}
}

func TestParseMountinfoLineNonNumericID(t *testing.T) {
	line := "abc 456 0:42 / /mnt rw - ext3 /dev/sda rw"
	_, _, ok := parseMountinfoLine(line)
	if ok {
		t.Error("non-numeric mountID should fail")
	}
}

func TestListFiltersSubmounts(t *testing.T) {
	mountinfo := strings.Join([]string{
		"100 0 0:1 / /tmp/fs rw - tmpfs tmpfs rw",
		"101 100 0:42 / /tmp/fs/web rw - overlay overlay rw",
		"102 101 0:43 / /tmp/fs/web/proc rw - proc proc rw",
		"103 101 0:44 / /tmp/fs/web/sys rw - sysfs sysfs rw",
		"104 101 0:45 / /tmp/fs/web/dev rw - devtmpfs devtmpfs rw",
		"105 100 0:46 / /tmp/fs/db rw - overlay overlay rw",
		"106 105 0:47 / /tmp/fs/db/proc rw - proc proc rw",
		"999 0 0:99 / /other/mount rw - ext4 /dev/sda rw",
	}, "\n") + "\n"

	mgr := NewManager("/tmp/fs", "/fake/helper")
	mounts, err := mgr.listFromScanner(bufio.NewScanner(strings.NewReader(mountinfo)))
	if err != nil {
		t.Fatal(err)
	}

	if len(mounts) != 2 {
		t.Fatalf("got %d mounts, want 2 (web + db)", len(mounts))
	}

	names := map[string]bool{}
	for _, m := range mounts {
		names[m.Name] = true
	}
	if !names["web"] {
		t.Error("missing 'web'")
	}
	if !names["db"] {
		t.Error("missing 'db'")
	}
	if names["proc"] || names["sys"] || names["dev"] {
		t.Error("submount leaked into List()")
	}
}

func TestListPrefixMatch(t *testing.T) {
	// /tmp/fs should not match /tmp/fstab
	mountinfo := "1 0 0:1 / /tmp/fstab rw - ext4 /dev/sda rw\n"

	mgr := NewManager("/tmp/fs", "/fake/helper")
	mounts, err := mgr.listFromScanner(bufio.NewScanner(strings.NewReader(mountinfo)))
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 0 {
		t.Errorf("got %d mounts, want 0 — /tmp/fstab should not match /tmp/fs prefix", len(mounts))
	}
}
