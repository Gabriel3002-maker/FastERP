package module

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"plugin"
	"strings"
	"sync"

	"github.com/fasterp/backend/internal/db"
)

type ModuleManager struct {
	mu          sync.RWMutex
	ModulesDir  string
	UploadDir   string
	wasmModules map[string]*WasmModule
}

func NewManager(modulesDir, uploadDir string) *ModuleManager {
	os.MkdirAll(modulesDir, 0755)
	os.MkdirAll(uploadDir, 0755)
	return &ModuleManager{
		ModulesDir:  modulesDir,
		UploadDir:   uploadDir,
		wasmModules: make(map[string]*WasmModule),
	}
}

func (m *ModuleManager) GetWasmModule(name string) *WasmModule {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.wasmModules[name]
}

type ModulePackage struct {
	Manifest   Manifest
	PluginPath string
	Frontend   []byte
	Checksum   string
}

type ModulePlugin interface {
	Register(mgr interface{}) error
}

func (m *ModuleManager) ExtractModulePackage(zipPath string) (*ModulePackage, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open zip: %w", err)
	}
	defer r.Close()

	pkg := &ModulePackage{}

	hasher := sha256.New()

	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}

		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}

		hasher.Write(data)

		switch {
		case f.Name == "manifest.json":
			if err := json.Unmarshal(data, &pkg.Manifest); err != nil {
				return nil, fmt.Errorf("invalid manifest: %w", err)
			}

		case strings.HasSuffix(f.Name, ".so"), strings.HasSuffix(f.Name, ".wasm"):
			dest := filepath.Join(m.ModulesDir, filepath.Base(f.Name))
			if err := os.WriteFile(dest, data, 0755); err != nil {
				return nil, err
			}
			pkg.PluginPath = dest

		case f.Name == "frontend.zip" || (strings.HasPrefix(f.Name, "frontend/") && strings.HasSuffix(f.Name, ".js")):
			if pkg.Frontend == nil {
				pkg.Frontend = data
			}
		}
	}

	pkg.Checksum = fmt.Sprintf("%x", hasher.Sum(nil))
	return pkg, nil
}

func (m *ModuleManager) LoadPlugin(moduleName string) error {
	// Try subdirectory structure first (new): modules/moduleName/module.wasm
	wasmPath := filepath.Join(m.ModulesDir, moduleName, "module.wasm")
	if _, err := os.Stat(wasmPath); err == nil {
		return m.loadWasmPlugin(wasmPath, moduleName)
	}

	// Fallback to flat structure: modules/moduleName.wasm
	wasmPath = filepath.Join(m.ModulesDir, moduleName+".wasm")
	if _, err := os.Stat(wasmPath); err == nil {
		return m.loadWasmPlugin(wasmPath, moduleName)
	}

	// Try .so plugin (legacy): modules/moduleName.so
	soPath := filepath.Join(m.ModulesDir, moduleName+".so")
	if _, err := os.Stat(soPath); err == nil {
		return m.loadSoPlugin(soPath, moduleName)
	}

	return fmt.Errorf("plugin not found: tried %s/module.wasm, %s.wasm, %s.so",
		moduleName, moduleName, moduleName)
}

func (m *ModuleManager) loadSoPlugin(path, moduleName string) error {
	p, err := plugin.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open plugin %s: %w", moduleName, err)
	}

	sym, err := p.Lookup("Register")
	if err != nil {
		return fmt.Errorf("plugin %s has no Register function: %w", moduleName, err)
	}

	registerFn, ok := sym.(func(interface{}) error)
	if !ok {
		registerFn2, ok2 := sym.(func())
		if !ok2 {
			return fmt.Errorf("plugin %s Register has unexpected signature", moduleName)
		}
		registerFn2()
	} else if err := registerFn(Global); err != nil {
		return fmt.Errorf("plugin %s registration failed: %w", moduleName, err)
	}

	return m.createModuleTables(moduleName)
}

func (m *ModuleManager) loadWasmPlugin(path, moduleName string) error {
	inst, wasmMod, err := LoadWasmModule(path)
	if err != nil {
		return fmt.Errorf("failed to load wasm module %s: %w", moduleName, err)
	}

	Global.Register(inst)
	m.mu.Lock()
	m.wasmModules[moduleName] = wasmMod
	m.mu.Unlock()

	return m.createModuleTables(moduleName)
}

func (m *ModuleManager) createModuleTables(moduleName string) error {
	inst := Global.Get(moduleName)
	if inst == nil {
		return nil
	}
	for _, model := range inst.Models {
		if !safeIdent(model.TableName) {
			return fmt.Errorf("unsafe table name: %q", model.TableName)
		}

		if _, err := db.DB.Exec(model.SQL); err != nil {
			return fmt.Errorf("failed to create table %s: %w", model.TableName, err)
		}
		log.Printf("[Modules] Created table: %s", model.TableName)
		db.DB.Exec(fmt.Sprintf("CREATE INDEX IF NOT EXISTS idx_%s_tenant ON %s(tenant_id)", model.TableName, model.TableName))

		// Enable RLS with tenant isolation policy
		db.ApplyTenantRLS(model.TableName)
	}
	log.Printf("[Modules] Loaded plugin: %s", moduleName)
	return nil
}

func (m *ModuleManager) UnloadPlugin(moduleName string) error {
	inst := Global.Get(moduleName)
	if inst == nil {
		return nil
	}

	// Drop model tables
	for _, model := range inst.Models {
		dropSQL := fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", model.TableName)
		if _, err := db.DB.Exec(dropSQL); err != nil {
			log.Printf("[Modules] Failed to drop table %s: %v", model.TableName, err)
		} else {
			log.Printf("[Modules] Dropped table: %s", model.TableName)
		}
	}

	if inst.OnUnload != nil {
		if err := inst.OnUnload(); err != nil {
			return err
		}
	}

	// Clean up wasm module if applicable
	m.mu.Lock()
	if wm, ok := m.wasmModules[moduleName]; ok {
		wm.CloseAll()
		delete(m.wasmModules, moduleName)
	}
	m.mu.Unlock()

	Global.Unregister(moduleName)
	log.Printf("[Modules] Unloaded plugin: %s", moduleName)
	return nil
}

func (m *ModuleManager) BuildModule(sourceDir, outputFile string, extra ...string) error {
	var backendRoot string
	if len(extra) > 0 {
		backendRoot = extra[0]
	}
	absDir, err := filepath.Abs(sourceDir)
	if err != nil {
		return fmt.Errorf("failed to resolve source dir: %w", err)
	}
	manifestPath := filepath.Join(absDir, "manifest.json")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		return fmt.Errorf("manifest.json not found in %s", sourceDir)
	}

	outFile := outputFile
	if outFile == "" {
		outFile = filepath.Base(sourceDir) + ".zip"
	}

	zipFile, err := os.Create(outFile)
	if err != nil {
		return err
	}
	defer zipFile.Close()

	zw := zip.NewWriter(zipFile)
	defer zw.Close()

	// Add manifest
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("invalid manifest: %w", err)
	}
	mf, err := zw.Create("manifest.json")
	if err != nil {
		return err
	}
	mf.Write(manifestData)

	// Build Go plugin or Wasm - compile from backend root to use same module context
	mainGo := filepath.Join(absDir, "main.go")
	if _, err := os.Stat(mainGo); err == nil {
		// Prefer .wasm build (cross-platform), fall back to .so (legacy)
		wasmFile := filepath.Join(os.TempDir(), manifest.Name+".wasm")
		soFile := filepath.Join(os.TempDir(), manifest.Name+".so")

		// Wasm build: use -buildmode=c-shared with Go >=1.25 toolchain
		wasmOK := false
		tmpDir, tmpErr := os.MkdirTemp("", "fasterp-wasm-*")
		if tmpErr == nil {
			defer os.RemoveAll(tmpDir)
			// Copy all .go files from source to temp dir
			entries, _ := os.ReadDir(absDir)
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
					data, _ := os.ReadFile(filepath.Join(absDir, e.Name()))
					os.WriteFile(filepath.Join(tmpDir, e.Name()), data, 0644)
				}
			}
			// Write go.mod with required toolchain (use safe module path)
			modName := strings.TrimLeft(strings.ReplaceAll(manifest.Name, "/", "_"), ".-_")
			if modName == "" {
				modName = "module"
			} else {
				modName = "fasterp/" + modName
			}
			gm := fmt.Sprintf("module %s\n\ngo 1.25.0\n\ntoolchain go1.25.11\n", modName)
			if backendRoot != "" {
				absBackend, _ := filepath.Abs(backendRoot)
				gm += fmt.Sprintf("require github.com/fasterp/backend/sdk/wasm v0.0.0\n")
				gm += fmt.Sprintf("replace github.com/fasterp/backend/sdk/wasm => %s/sdk/wasm\n", absBackend)
			}
			os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(gm), 0644)

			cmd := exec.Command("go", "build",
				"-buildmode=c-shared",
				"-o", wasmFile,
				".",
			)
			cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOTOOLCHAIN=go1.25.11")
			cmd.Dir = tmpDir
			if out, err := cmd.CombinedOutput(); err == nil {
				wasmData, _ := os.ReadFile(wasmFile)
				sf, _ := zw.Create(manifest.Name + ".wasm")
				sf.Write(wasmData)
				wasmOK = true
			} else {
				log.Printf("[Modules] Wasm build failed (c-shared): %s\n  %v", string(out), err)
			}
		} else {
			log.Printf("[Modules] Cannot create temp dir for wasm build: %v", tmpErr)
		}

		if !wasmOK {
			// Fall back to plugin build
			buildDir := absDir
			if backendRoot != "" {
				buildDir = backendRoot
			}
			cmd2 := exec.Command("go", "build",
				"-buildmode=plugin",
				"-o", soFile,
				mainGo,
			)
			cmd2.Dir = buildDir
			if out2, err := cmd2.CombinedOutput(); err != nil {
				return fmt.Errorf("build failed (plugin fallback): %s: %w", string(out2), err)
			}
			soData, _ := os.ReadFile(soFile)
			sf, _ := zw.Create(manifest.Name + ".so")
			sf.Write(soData)
		}
	}

	// Add frontend
	frontendDir := filepath.Join(absDir, "frontend")
	if stat, err := os.Stat(frontendDir); err == nil && stat.IsDir() {
		filepath.Walk(frontendDir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			relPath, _ := filepath.Rel(absDir, path)
			f, err := zw.Create(relPath)
			if err != nil {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			f.Write(data)
			return nil
		})
	}

	log.Printf("[Modules] Built module package: %s", outFile)
	return nil
}
