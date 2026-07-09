// Package mount manages the lifecycle of exported container filesystem mounts.
// It calls the docker-mount-helper C binary for the actual namespace operations.
package mount

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"log/slog"
)

// Info represents an exported container mount.
type Info struct {
	Name    string // container name (from target path basename)
	Target  string // e.g. /opt/mount/web-php
	NSInode uint64 // mount namespace inode of the container at export time
	MountID int    // mount ID from /proc/self/mountinfo
}

// Manager manages exported container mounts under a target directory.
type Manager struct {
	TargetDir  string // e.g. "/opt/mount"
	HelperPath string // path to docker-mount-helper binary
}

// NewManager returns a Manager for the given target directory and helper.
func NewManager(targetDir, helperPath string) *Manager {
	return &Manager{
		TargetDir:  targetDir,
		HelperPath: helperPath,
	}
}

// Export creates a new mount for the container by delegating to the C helper.
func (m *Manager) Export(name string, pid int) error {
	targetPath := filepath.Join(m.TargetDir, name)

	slog.Info("exporting container mount",
		"name", name,
		"pid", pid,
		"target", targetPath,
	)

	cmd := exec.Command(m.HelperPath, strconv.Itoa(pid), targetPath)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("helper export %s (pid %d): %w", name, pid, err)
	}

	return nil
}

func (m *Manager) Replace(name string, pid int) error {
	return m.Export(name, pid)
}

// CleanupAll removes all mounts and empty directories under TargetDir.
// Container mounts are logged individually; submount artifacts and empty
// directories are cleaned up silently.
func (m *Manager) CleanupAll() {
	mounts, _ := m.List()

	for _, mi := range mounts {
		slog.Info("unmounting", "name", mi.Name, "target", mi.Target)
		exec.Command("umount", "-R", "-l", mi.Target).Run()
		os.Remove(mi.Target)
	}

	entries, _ := os.ReadDir(m.TargetDir)
	for _, e := range entries {
		if e.IsDir() {
			os.Remove(filepath.Join(m.TargetDir, e.Name()))
		}
	}
}

// Cleanup recursively unmounts the container export and removes the empty
// directory. Returns nil even if the target is already gone.
func (m *Manager) Cleanup(name string) error {
	targetPath := filepath.Join(m.TargetDir, name)

	if !m.IsMounted(name) {
		os.Remove(targetPath)
		return nil
	}

	slog.Info("cleaning up mount", "name", name, "target", targetPath)
	m.unmountLazy(name)
	os.Remove(targetPath)
	return nil
}

// unmountLazy performs a lazy recursive umount via "umount -R -l <target>".
func (m *Manager) unmountLazy(name string) error {
	targetPath := filepath.Join(m.TargetDir, name)
	return exec.Command("umount", "-R", "-l", targetPath).Run()
}

// List returns all currently exported mounts under TargetDir by parsing
// /proc/self/mountinfo.
func (m *Manager) List() ([]Info, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, fmt.Errorf("open /proc/self/mountinfo: %w", err)
	}
	defer f.Close()

	mounts, err := m.listFromScanner(bufio.NewScanner(f))
	if err != nil {
		return nil, err
	}
	return mounts, nil
}

// listFromScanner reads mountinfo lines from a scanner and returns direct
// children of TargetDir.
func (m *Manager) listFromScanner(scanner *bufio.Scanner) ([]Info, error) {
	var mounts []Info
	prefix := m.TargetDir + "/"

	for scanner.Scan() {
		line := scanner.Text()

		mountID, mountPoint, ok := parseMountinfoLine(line)
		if !ok {
			continue
		}

		if !strings.HasPrefix(mountPoint, prefix) {
			continue
		}

		if filepath.Dir(mountPoint) != m.TargetDir {
			continue
		}

		name := filepath.Base(mountPoint)
		mounts = append(mounts, Info{
			Name:    name,
			Target:  mountPoint,
			MountID: mountID,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan mountinfo: %w", err)
	}

	return mounts, nil
}

// IsMounted checks whether a container is currently exported at TargetDir/name.
func (m *Manager) IsMounted(name string) bool {
	mounts, err := m.List()
	if err != nil {
		slog.Error("IsMounted: list failed", "error", err)
		return false
	}
	for _, mi := range mounts {
		if mi.Name == name {
			return true
		}
	}
	return false
}

// parseMountinfoLine extracts mountID and mountPoint from a single
// /proc/self/mountinfo line. The "-" field separates optional fields from
// the fixed tail (fstype, source, superOptions).
func parseMountinfoLine(line string) (mountID int, mountPoint string, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 7 {
		return 0, "", false
	}

	// Locate the "-" separator. Everything between field 6 (options) and
	// the separator is optional.
	sepIdx := -1
	for i := 5; i < len(fields); i++ {
		if fields[i] == "-" {
			sepIdx = i
			break
		}
	}
	if sepIdx < 0 {
		return 0, "", false
	}

	id, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, "", false
	}

	return id, fields[4], true
}
