package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestModuleSeedAssetPathUsesManifest(t *testing.T) {
	dir := t.TempDir()
	modDir := filepath.Join(dir, "contacts")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatalf("mkdir module dir: %v", err)
	}
	manifestPath := filepath.Join(modDir, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"name":"contacts"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	if got := moduleSeedAssetPath(dir, "contacts"); got != manifestPath {
		t.Fatalf("moduleSeedAssetPath(dir, \"contacts\") = %q, want %q", got, manifestPath)
	}
}
