// Package watch reconciles the desired state (containers discovered by the
// runtime) with the actual state (mounts exported by the mount manager).
package watch

import (
	"context"
	"fmt"
	"sync"
	"time"

	"log/slog"

	"mountns/internal/mount"
	"mountns/internal/ns"
	"mountns/internal/runtime"
)

// Watcher reconciles desired state with actual state on a loop.
type Watcher struct {
	Runtime  runtime.ContainerRuntime
	MountMgr *mount.Manager
	Interval time.Duration // poll interval for full reconciliation

	mu       sync.Mutex
	exported map[string]*ns.MountNS // container name → exported NS (in-memory only)
}

// NewWatcher returns a Watcher ready for use.
func NewWatcher(rt runtime.ContainerRuntime, mgr *mount.Manager, interval time.Duration) *Watcher {
	return &Watcher{
		Runtime:  rt,
		MountMgr: mgr,
		Interval: interval,
		exported: make(map[string]*ns.MountNS),
	}
}

// Run starts the watch loop and blocks until ctx is cancelled.
//
// Algorithm:
//  1. Subscribe to Docker events (event-driven)
//  2. Perform initial full reconcile
//  3. Loop: wait for either an event or a poll timer tick
//  4. On event → reconcile the specific container
//  5. On timer tick → full reconcile all containers
func (w *Watcher) Run(ctx context.Context) error {
	eventCh, err := w.Runtime.Events(ctx)
	if err != nil {
		return fmt.Errorf("events subscription: %w", err)
	}

	slog.Info("performing initial full reconciliation")
	if err := w.reconcileAll(); err != nil {
		slog.Error("initial reconciliation failed", "error", err)
	}

	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("watch loop: context cancelled, shutting down")
			return ctx.Err()

		case evt, ok := <-eventCh:
			if !ok {
				slog.Warn("docker events channel closed, will rely on polling")
				// Nil the channel so select skips it.
				eventCh = nil
				continue
			}
			slog.Debug("received event", "type", evt.Type, "name", evt.Name)
			if err := w.reconcileOne(evt.Name); err != nil {
				slog.Error("reconcileOne failed", "name", evt.Name, "error", err)
			}

		case <-ticker.C:
			slog.Debug("poll ticker: full reconciliation")
			if err := w.reconcileAll(); err != nil {
				slog.Error("reconcileAll failed", "error", err)
			}
		}
	}
}

// reconcileAll performs a full reconciliation of all containers.
//
//  1. List containers from runtime
//  2. List mounted exports from mount manager
//  3. For each container: export if not mounted; replace if namespace changed
//  4. For each mounted export whose container is gone → cleanup
func (w *Watcher) reconcileAll() error {
	containers, err := w.Runtime.List()
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	mounts, err := w.MountMgr.List()
	if err != nil {
		return fmt.Errorf("list mounts: %w", err)
	}

	// Index containers by name.
	containerByName := make(map[string]runtime.Container, len(containers))
	for _, c := range containers {
		containerByName[c.Name] = c
	}

	// Index mounts by name.
	mountByName := make(map[string]mount.Info, len(mounts))
	for _, m := range mounts {
		mountByName[m.Name] = m
	}

	// Reconcile containers: ensure each is exported.
	for _, c := range containers {
		w.mu.Lock()
		exportedNS, hasExport := w.exported[c.Name]
		w.mu.Unlock()

		if _, isMounted := mountByName[c.Name]; !isMounted {
			// Not mounted at all — export.
			if err := w.exportAndTrack(c.Name, c.PID); err != nil {
				slog.Error("export failed", "name", c.Name, "error", err)
			}
			continue
		}

		// Mounted — check if namespace changed.
		currentNS, err := ns.Open(c.PID)
		if err != nil {
			slog.Warn("cannot open container NS, will retry", "name", c.Name, "pid", c.PID, "error", err)
			continue
		}

		if hasExport && !exportedNS.Equal(currentNS) {
			slog.Info("namespace changed, replacing mount", "name", c.Name)
			if err := w.MountMgr.Replace(c.Name, c.PID); err != nil {
				slog.Error("replace mount failed", "name", c.Name, "error", err)
				currentNS.Close()
				continue
			}
			w.mu.Lock()
			if old, ok := w.exported[c.Name]; ok {
				old.Close()
			}
			w.exported[c.Name] = currentNS
			w.mu.Unlock()
		} else if !hasExport {
			// Mounted but not tracked (daemon restart).
			w.mu.Lock()
			w.exported[c.Name] = currentNS
			w.mu.Unlock()
		} else {
			// Namespace unchanged, tracked — nothing to do.
			currentNS.Close()
		}
	}

	// Cleanup mounts for containers that no longer exist.
	for name := range mountByName {
		if _, exists := containerByName[name]; !exists {
			slog.Info("container gone, cleaning up mount", "name", name)
			if err := w.MountMgr.Cleanup(name); err != nil {
				slog.Error("cleanup failed", "name", name, "error", err)
			}
			w.mu.Lock()
			if ns, ok := w.exported[name]; ok {
				ns.Close()
				delete(w.exported, name)
			}
			w.mu.Unlock()
		}
	}

	return nil
}

// reconcileOne reconciles a single container by name.
func (w *Watcher) reconcileOne(name string) error {
	pid, err := w.Runtime.GetPID(name)
	if err != nil {
		// Container may be gone — cleanup.
		slog.Info("container not found, cleaning up", "name", name, "error", err)
		if cleanupErr := w.MountMgr.Cleanup(name); cleanupErr != nil {
			slog.Error("cleanup failed", "name", name, "error", cleanupErr)
		}
		w.mu.Lock()
		if ns, ok := w.exported[name]; ok {
			ns.Close()
			delete(w.exported, name)
		}
		w.mu.Unlock()
		return nil
	}

	w.mu.Lock()
	exportedNS, hasExport := w.exported[name]
	w.mu.Unlock()

	currentNS, err := ns.Open(pid)
	if err != nil {
		return fmt.Errorf("open ns for %s (pid %d): %w", name, pid, err)
	}

	if !hasExport {
		if err := w.exportAndTrackWithNS(name, pid, currentNS); err != nil {
			currentNS.Close()
			return fmt.Errorf("export %s: %w", name, err)
		}
		return nil
	}

	if !exportedNS.Equal(currentNS) {
		slog.Info("namespace changed, replacing mount", "name", name)
		if err := w.MountMgr.Replace(name, pid); err != nil {
			currentNS.Close()
			return fmt.Errorf("replace %s: %w", name, err)
		}
		w.mu.Lock()
		if old, ok := w.exported[name]; ok {
			old.Close()
		}
		w.exported[name] = currentNS
		w.mu.Unlock()
		return nil
	}

	// Namespace unchanged.
	currentNS.Close()
	return nil
}

// exportAndTrack exports the container and records the exported namespace
// in the in-memory map.
func (w *Watcher) exportAndTrack(name string, pid int) error {
	currentNS, err := ns.Open(pid)
	if err != nil {
		return fmt.Errorf("open ns for %s (pid %d): %w", name, pid, err)
	}

	if err := w.MountMgr.Export(name, pid); err != nil {
		currentNS.Close()
		return fmt.Errorf("export %s: %w", name, err)
	}

	w.mu.Lock()
	w.exported[name] = currentNS
	w.mu.Unlock()
	return nil
}

// exportAndTrackWithNS is like exportAndTrack but uses an already-opened
// MountNS to avoid an extra open call. Caller provides the PID.
func (w *Watcher) exportAndTrackWithNS(name string, pid int, currentNS *ns.MountNS) error {
	if err := w.MountMgr.Export(name, pid); err != nil {
		currentNS.Close()
		return fmt.Errorf("export %s: %w", name, err)
	}

	w.mu.Lock()
	w.exported[name] = currentNS
	w.mu.Unlock()
	return nil
}
