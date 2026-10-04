package handlers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadModulesFromFilesystemUsesConfiguredDirAndYAML(t *testing.T) {
	dir := t.TempDir()

	for _, mod := range []string{"hello", "contacts"} {
		if err := os.MkdirAll(filepath.Join(dir, mod), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", mod, err)
		}
		manifest := "name: " + mod + "\nlabel: " + mod + "\nversion: 1.0.0\nauthor: FastERP Team\ndescription: test module\n"
		if err := os.WriteFile(filepath.Join(dir, mod, "module.yaml"), []byte(manifest), 0o644); err != nil {
			t.Fatalf("write %s manifest: %v", mod, err)
		}
	}

	h := NewModuleHandler(nil, dir)
	mods := h.loadModulesFromFilesystem()
	if len(mods) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(mods))
	}

	seen := map[string]bool{}
	for _, m := range mods {
		seen[m.Name] = true
	}
	if !seen["hello"] || !seen["contacts"] {
		t.Fatalf("expected hello and contacts modules, got %#v", seen)
	}
}
