package runtime

import (
	"bufio"
	"strconv"
	"strings"
	"testing"
)

func parseInspectLine(line string) (name, id string, pid int, ok bool) {
	parts := strings.Split(line, "\t")
	if len(parts) < 3 {
		return "", "", 0, false
	}
	p, err := strconv.Atoi(parts[2])
	if err != nil || p == 0 {
		return "", "", 0, false
	}
	return parts[0], parts[1], p, true
}

func parseEventsLine(line string) (evtType, name string, ok bool) {
	parts := strings.Split(line, "\t")
	if len(parts) < 3 {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func TestParseInspectOutput(t *testing.T) {
	// docker inspect --format '{{slice .Name 1}}\t{{.ID}}\t{{.State.Pid}}' <ids...>
	output := "web-php\ta1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6\t12345\npostgres\tb2c3d4e5f6g7h8i9j0k1l2m3n4o5p6q7\t12346\n"

	scanner := bufio.NewScanner(strings.NewReader(output))
	var containers []Container
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		name, id, pid, ok := parseInspectLine(line)
		if !ok {
			t.Errorf("failed to parse: %q", line)
			continue
		}
		containers = append(containers, Container{Name: name, ID: id, PID: pid})
	}

	if len(containers) != 2 {
		t.Fatalf("got %d containers, want 2", len(containers))
	}

	if containers[0].Name != "web-php" || containers[0].PID != 12345 {
		t.Errorf("container 0: name=%q pid=%d", containers[0].Name, containers[0].PID)
	}
	if containers[1].Name != "postgres" || containers[1].PID != 12346 {
		t.Errorf("container 1: name=%q pid=%d", containers[1].Name, containers[1].PID)
	}
}

func TestParseInspectOutputLeadingSlash(t *testing.T) {
	// docker inspect often returns names with leading /
	// {{slice .Name 1}} removes it
	output := "web-php\tabc123\t42\n"
	name, _, pid, ok := parseInspectLine(strings.TrimSpace(output))
	if !ok {
		t.Fatal("parse failed")
	}
	if name != "web-php" {
		t.Errorf("name = %q, want web-php", name)
	}
	if pid != 42 {
		t.Errorf("pid = %d, want 42", pid)
	}
}

func TestParseEventsOutput(t *testing.T) {
	// docker events --format '{{.Type}}\t{{.Action}}\t{{index .Actor.Attributes "name"}}'
	lines := []struct {
		input    string
		wantType string
		wantName string
		wantOK   bool
	}{
		{"container\tstart\tweb-php", "start", "web-php", true},
		{"container\tdie\tpostgres", "die", "postgres", true},
		{"container\tdestroy\told-app", "destroy", "old-app", true},
		{"container\trename\tnew-name", "rename", "new-name", true},
		{"", "", "", false},
		{"container\tstart", "", "", false},
	}

	for _, tt := range lines {
		evtType, name, ok := parseEventsLine(tt.input)
		if ok != tt.wantOK {
			t.Errorf("line=%q: ok=%v, want %v", tt.input, ok, tt.wantOK)
			continue
		}
		if !ok {
			continue
		}
		if evtType != tt.wantType {
			t.Errorf("line=%q: type=%q, want %q", tt.input, evtType, tt.wantType)
		}
		if name != tt.wantName {
			t.Errorf("line=%q: name=%q, want %q", tt.input, name, tt.wantName)
		}
	}
}

func TestParseInspectOutputEmpty(t *testing.T) {
	output := ""
	scanner := bufio.NewScanner(strings.NewReader(output))
	count := 0
	for scanner.Scan() {
		count++
	}
	if count != 0 {
		t.Errorf("empty output should yield 0 containers, got %d", count)
	}
}

func TestParseInspectOutputStopped(t *testing.T) {
	_, _, pid, ok := parseInspectLine("stopped-app\tabc123\t0")
	if ok {
		t.Error("PID 0 should be treated as not running")
	}
	_ = pid
}

func TestParseGetPIDOutput(t *testing.T) {
	// docker inspect --format '{{.State.Pid}}' <name>
	tests := []struct {
		output  string
		wantPID int
		wantErr bool
	}{
		{"12345\n", 12345, false},
		{"0\n", 0, true},
		{"\n", 0, true},
		{"not-a-number\n", 0, true},
	}

	for _, tt := range tests {
		pid, err := parsePID(tt.output)
		if tt.wantErr && err == nil {
			t.Errorf("output=%q: expected error, got pid=%d", tt.output, pid)
		}
		if !tt.wantErr && err != nil {
			t.Errorf("output=%q: unexpected error: %v", tt.output, err)
		}
		if !tt.wantErr && pid != tt.wantPID {
			t.Errorf("output=%q: pid=%d, want %d", tt.output, pid, tt.wantPID)
		}
	}
}

func parsePID(output string) (int, error) {
	pidStr := strings.TrimSpace(output)
	if pidStr == "" || pidStr == "0" {
		return 0, strconv.ErrSyntax
	}
	return strconv.Atoi(pidStr)
}
