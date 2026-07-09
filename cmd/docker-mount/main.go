// docker-mount is the main daemon for the docker-mount project. It watches
// Docker container lifecycle events and exports container filesystems to the
// host filesystem under a configurable target directory.
//
// Usage (daemon mode):
//
//	docker-mount --target /opt/mount
//
// Subcommands:
//
//	docker-mount list
//	docker-mount info <container-name>
//	docker-mount cat <container-name> <path>
//	docker-mount exec <container-name> <command...>
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"log/slog"

	"mountns/internal/mount"
	"mountns/internal/ns"
	"mountns/internal/runtime"
	"mountns/internal/watch"
)

func main() {
	// Daemon flags.
	targetDir := flag.String("target", "/opt/mount", "target directory for mounts")
	helperPath := flag.String("helper", "./docker-mount-helper", "path to C helper binary")
	interval := flag.Duration("interval", 30*time.Second, "poll reconciliation interval")
	cleanupOnExit := flag.Bool("cleanup-on-exit", true, "unmount all exports on shutdown (default true)")

	flag.Parse()

	args := flag.Args()

	// If a subcommand is provided, handle it and exit (no daemon mode).
	if len(args) > 0 {
		handleSubcommand(args, *targetDir, *helperPath)
		return
	}

	// --- Daemon mode ---

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	absTarget, err := filepath.Abs(*targetDir)
	if err != nil {
		slog.Error("cannot resolve target path", "path", *targetDir, "error", err)
		os.Exit(1)
	}
	*targetDir = absTarget

	if err := checkPrerequisites(*targetDir, *helperPath); err != nil {
		slog.Error("prerequisite check failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	rt := runtime.NewDockerRuntime()
	mgr := mount.NewManager(*targetDir, *helperPath)
	w := watch.NewWatcher(rt, mgr, *interval)

	slog.Info("starting docker-mount daemon",
		"target", *targetDir,
		"helper", *helperPath,
		"interval", interval.String(),
	)

	if containers, err := rt.List(); err == nil {
		names := make(map[string]bool, len(containers))
		for _, c := range containers {
			names[c.Name] = true
		}
		mounts, _ := mgr.List()
		for _, m := range mounts {
			if !names[m.Name] {
				slog.Info("cleaning orphan mount", "name", m.Name)
				mgr.Cleanup(m.Name)
			}
		}
	}

	if err := w.Run(ctx); err != nil && err != context.Canceled {
		slog.Error("watcher failed", "error", err)
		os.Exit(1)
	}

	if *cleanupOnExit {
		slog.Info("cleaning up all mounts on exit")
		mgr.CleanupAll()
	}

	slog.Info("shutdown complete")
}

// handleSubcommand dispatches to a CLI subcommand.
func handleSubcommand(args []string, targetDir, helperPath string) {
	subcmd := args[0]
	rest := args[1:]

	switch subcmd {
	case "list":
		cmdList(targetDir)
	case "info":
		if len(rest) < 1 {
			fmt.Fprintln(os.Stderr, "usage: docker-mount info <container-name>")
			os.Exit(1)
		}
		cmdInfo(rest[0], targetDir)
	case "cat":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "usage: docker-mount cat <container-name> <path>")
			os.Exit(1)
		}
		cmdCat(rest[0], rest[1])
	case "exec":
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "usage: docker-mount exec <container-name> <command...>")
			os.Exit(1)
		}
		cmdExec(rest[0], rest[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", subcmd)
		fmt.Fprintln(os.Stderr, "available: list, info, cat, exec")
		os.Exit(1)
	}
}

// cmdList prints all exported mounts discovered under targetDir.
func cmdList(targetDir string) {
	mgr := mount.NewManager(targetDir, "")
	mounts, err := mgr.List()
	if err != nil {
		fmt.Fprintf(os.Stderr, "list: %v\n", err)
		os.Exit(1)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTARGET\tMOUNT_ID")
	for _, m := range mounts {
		fmt.Fprintf(w, "%s\t%s\t%d\n", m.Name, m.Target, m.MountID)
	}
	w.Flush()
}

// cmdInfo prints metadata for a specific container.
func cmdInfo(name, targetDir string) {
	rt := runtime.NewDockerRuntime()
	mgr := mount.NewManager(targetDir, "")

	pid, err := rt.GetPID(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "info %s: %v\n", name, err)
		os.Exit(1)
	}

	nsEntry, err := ns.Open(pid)
	if err != nil {
		fmt.Fprintf(os.Stderr, "info %s: open namespace: %v\n", name, err)
		os.Exit(1)
	}
	defer nsEntry.Close()

	// Get container ID and image via docker inspect.
	id, image := inspectContainer(name)

	mounted := "no"
	if mgr.IsMounted(name) {
		mounted = "yes"
	}

	fmt.Printf("Name:        %s\n", name)
	fmt.Printf("PID:         %d\n", pid)
	fmt.Printf("Namespace:   mnt:[%d]\n", nsEntry.Inode)
	fmt.Printf("Image:       %s\n", image)
	fmt.Printf("Container:   %s\n", id)
	fmt.Printf("Target:      %s/%s\n", targetDir, name)
	fmt.Printf("Mounted:     %s\n", mounted)
}

// cmdCat reads a file from a container via /proc/<pid>/root/<path> without
// requiring an exported mount.
func cmdCat(name, containerPath string) {
	rt := runtime.NewDockerRuntime()

	pid, err := rt.GetPID(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cat %s: %v\n", name, err)
		os.Exit(1)
	}

	rootPath := fmt.Sprintf("/proc/%d/root%s", pid, containerPath)
	data, err := os.ReadFile(rootPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cat %s: %v\n", name, err)
		os.Exit(1)
	}

	os.Stdout.Write(data)
}

// cmdExec runs a command inside a container via docker exec.
func cmdExec(name string, command []string) {
	args := append([]string{"exec", name}, command...)
	cmd := exec.Command("docker", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "exec %s: %v\n", name, err)
		os.Exit(1)
	}
}

// inspectContainer runs "docker inspect" to retrieve the full container ID
// and image name. Returns "unknown" for fields that cannot be retrieved.
func inspectContainer(name string) (id, image string) {
	cmd := exec.Command("docker", "inspect",
		"--format", "{{.ID}}\t{{.Config.Image}}",
		name,
	)
	out, err := cmd.Output()
	if err != nil {
		return "unknown", "unknown"
	}

	parts := strings.Split(strings.TrimSpace(string(out)), "\t")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	id = "unknown"
	image = "unknown"
	if len(parts) >= 1 && parts[0] != "" {
		id = parts[0]
	}
	if len(parts) >= 2 && parts[1] != "" {
		image = parts[1]
	}
	return
}

func checkPrerequisites(targetDir, helperPath string) error {
	if _, err := os.Stat(targetDir); err != nil {
		return fmt.Errorf("target directory %q does not exist: %w", targetDir, err)
	}

	if _, err := os.Stat(helperPath); err != nil {
		return fmt.Errorf("helper binary %q not found: %w", helperPath, err)
	}

	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker not found in PATH: %w", err)
	}

	if !hasCapSysAdmin() {
		return fmt.Errorf("CAP_SYS_ADMIN required — run as root or grant the capability")
	}

	return nil
}

func hasCapSysAdmin() bool {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "CapEff:\t"); ok {
			caps, err := strconv.ParseUint(strings.TrimSpace(rest), 16, 64)
			if err != nil {
				return false
			}
			return caps&(1<<21) != 0
		}
	}
	return false
}
