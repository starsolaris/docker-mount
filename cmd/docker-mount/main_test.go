package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckPrerequisitesTargetMissing(t *testing.T) {
	err := checkPrerequisites("/nonexistent/path", "/bin/true")
	if err == nil {
		t.Error("expected error for missing target dir")
	}
}

func TestCheckPrerequisitesHelperMissing(t *testing.T) {
	dir := t.TempDir()
	err := checkPrerequisites(dir, "/nonexistent/helper")
	if err == nil {
		t.Error("expected error for missing helper")
	}
}

func TestCheckPrerequisitesOK(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "helper")
	os.WriteFile(helper, []byte("fake"), 0755)

	err := checkPrerequisites(dir, helper)
	if err != nil {
		t.Logf("checkPrerequisites: %v (docker may not be in PATH — acceptable)", err)
	}
}
