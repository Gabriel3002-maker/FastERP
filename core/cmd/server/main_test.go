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

func TestModuleSeedPresetsIncludesAvailableModules(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"hello", "contacts"} {
		modDir := filepath.Join(dir, name)
		if err := os.MkdirAll(modDir, 0o755); err != nil {
			t.Fatalf("mkdir module dir %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(modDir, "module.yaml"), []byte("name: "+name+"\nlabel: "+name+"\nversion: 1.0.0\n"), 0o644); err != nil {
			t.Fatalf("write manifest for %s: %v", name, err)
		}
	}

	got := moduleSeedPresets(dir)
	seen := map[string]bool{}
	for _, p := range got {
		seen[p.name] = true
	}
	if !seen["hello"] || !seen["contacts"] {
		t.Fatalf("moduleSeedPresets() = %#v, want hello and contacts present", got)
	}
	for _, p := range got {
		if !p.active {
			t.Fatalf("moduleSeedPresets() should default to active modules for the default tenant, got %#v", got)
		}
	}
}
