// Package runtime defines the container runtime abstraction used by the
// docker-mount daemon to discover containers, retrieve their PIDs, and
// subscribe to lifecycle events.
package runtime

import "context"

// Container represents a running container known to the runtime.
type Container struct {
	Name string // e.g. "web-php"
	ID   string // full container ID
	PID  int    // process ID of the container init process
}

// Event represents a container lifecycle event received from the runtime.
type Event struct {
	Type string // "start", "die", "destroy", "rename"
	Name string // container name
}

// ContainerRuntime is the interface that every supported runtime must implement.
type ContainerRuntime interface {
	// List returns all running containers.
	List() ([]Container, error)

	// GetPID returns the PID for the container identified by name.
	GetPID(name string) (int, error)

	// Events returns a channel that delivers container lifecycle events.
	// The channel is closed when ctx is cancelled or the underlying
	// event stream terminates.
	Events(ctx context.Context) (<-chan Event, error)
}
