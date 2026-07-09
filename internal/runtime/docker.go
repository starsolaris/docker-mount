package runtime

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"log/slog"
)

// DockerRuntime implements ContainerRuntime using the docker CLI via os/exec.
type DockerRuntime struct{}

// NewDockerRuntime returns a DockerRuntime ready for use.
func NewDockerRuntime() *DockerRuntime {
	return &DockerRuntime{}
}

// List returns all running containers by querying docker ps for IDs,
// then docker inspect for names and PIDs (docker ps --format does not
// expose State.Pid as a struct field in newer Docker versions).
func (d *DockerRuntime) List() ([]Container, error) {
	idsOut, err := exec.Command("docker", "ps", "-q", "--no-trunc").Output()
	if err != nil {
		return nil, fmt.Errorf("docker ps: %w", err)
	}

	ids := strings.Fields(string(idsOut))
	if len(ids) == 0 {
		return nil, nil
	}

	args := append([]string{"inspect", "--format", "{{slice .Name 1}}\t{{.ID}}\t{{.State.Pid}}"}, ids...)
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("docker inspect: %w", err)
	}

	var containers []Container
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			slog.Warn("docker inspect: unexpected output", "line", line)
			continue
		}

		pid, err := strconv.Atoi(parts[2])
		if err != nil || pid == 0 {
			slog.Warn("docker inspect: invalid PID", "line", line)
			continue
		}

		containers = append(containers, Container{
			Name: parts[0],
			ID:   parts[1],
			PID:  pid,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan inspect output: %w", err)
	}

	return containers, nil
}

// GetPID runs "docker inspect" to retrieve the PID of a specific container.
func (d *DockerRuntime) GetPID(name string) (int, error) {
	cmd := exec.Command("docker", "inspect",
		"--format", "{{.State.Pid}}",
		name,
	)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("docker inspect %s: %w", name, err)
	}

	pidStr := strings.TrimSpace(string(out))
	if pidStr == "" || pidStr == "0" {
		return 0, fmt.Errorf("container %s is not running", name)
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("docker inspect %s: invalid PID %q: %w", name, pidStr, err)
	}

	return pid, nil
}

// Events subscribes to Docker container lifecycle events. The returned channel
// delivers Event values until ctx is cancelled, at which point the channel is
// closed.
func (d *DockerRuntime) Events(ctx context.Context) (<-chan Event, error) {
	// Use CommandContext so the process is killed when ctx is done.
	cmd := exec.CommandContext(ctx, "docker", "events",
		"--format", "{{.Type}}\t{{.Action}}\t{{index .Actor.Attributes \"name\"}}",
		"--filter", "type=container",
		"--filter", "event=start",
		"--filter", "event=die",
		"--filter", "event=destroy",
		"--filter", "event=rename",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("docker events: stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("docker events: start: %w", err)
	}

	ch := make(chan Event)
	go func() {
		defer close(ch)
		defer func() {
			// Drain stdout so the process can exit cleanly.
			_ = cmd.Wait()
		}()

		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}

			parts := strings.Split(line, "\t")
			if len(parts) < 3 {
				slog.Warn("docker events: unexpected line", "line", line)
				continue
			}

			evt := Event{
				Type: parts[1],
				Name: parts[2],
			}

			select {
			case ch <- evt:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			// Context cancellation is expected; only log real scanner errors.
			if ctx.Err() == nil {
				slog.Error("docker events: scanner error", "error", err)
			}
		}
	}()

	return ch, nil
}
