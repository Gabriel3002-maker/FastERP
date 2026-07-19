package module

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type WasmRuntime struct {
	ctx    context.Context
	runtime wazero.Runtime
}

func NewWasmRuntime() *WasmRuntime {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	wasi_snapshot_preview1.MustInstantiate(ctx, r)
	return &WasmRuntime{ctx: ctx, runtime: r}
}

func (wr *WasmRuntime) Close() { wr.runtime.Close(wr.ctx) }

type WasmModule struct {
	wr       *WasmRuntime
	compiled wazero.CompiledModule
	instance api.Module
	name     string
}

func (wr *WasmRuntime) LoadModule(path string) (*WasmModule, error) {
	wasmBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read wasm file: %w", err)
	}

	compiled, err := wr.runtime.CompileModule(wr.ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to compile wasm: %w", err)
	}

	name := strings.TrimSuffix(filepath.Base(path), ".wasm")

	instance, err := wr.runtime.InstantiateModule(wr.ctx, compiled,
		wazero.NewModuleConfig().WithName(name).WithStartFunctions("_initialize"))
	if err != nil {
		compiled.Close(wr.ctx)
		return nil, fmt.Errorf("failed to instantiate wasm module %s: %w", name, err)
	}

	return &WasmModule{
		wr:       wr,
		compiled: compiled,
		instance: instance,
		name:     name,
	}, nil
}

func (wm *WasmModule) Close() {
	if wm.instance != nil {
		wm.instance.Close(wm.wr.ctx)
	}
	if wm.compiled != nil {
		wm.compiled.Close(wm.wr.ctx)
	}
}

func (wm *WasmModule) CloseAll() {
	wm.Close()
	wm.wr.Close()
}

func (wm *WasmModule) Name() string { return wm.name }

func (wm *WasmModule) getExport(name string) api.Function {
	return wm.instance.ExportedFunction(name)
}

func (wm *WasmModule) callExport(name string, args ...uint64) ([]uint64, error) {
	fn := wm.getExport(name)
	if fn == nil {
		return nil, fmt.Errorf("export %s not found in module %s", name, wm.name)
	}
	return fn.Call(wm.wr.ctx, args...)
}

func (wm *WasmModule) malloc(size uint32) (uint32, error) {
	fn := wm.getExport("malloc")
	if fn == nil {
		return 0, fmt.Errorf("module %s does not export malloc", wm.name)
	}
	results, err := fn.Call(wm.wr.ctx, uint64(size))
	if err != nil {
		return 0, fmt.Errorf("malloc failed: %w", err)
	}
	if len(results) < 1 {
		return 0, fmt.Errorf("malloc returned no results")
	}
	return uint32(results[0]), nil
}

func (wm *WasmModule) writeToMemory(data []byte) (uint32, error) {
	ptr, err := wm.malloc(uint32(len(data)))
	if err != nil {
		return 0, err
	}
	if !wm.instance.Memory().Write(ptr, data) {
		return 0, fmt.Errorf("failed to write to wasm memory at offset %d", ptr)
	}
	return ptr, nil
}

func (wm *WasmModule) readMemory(offset, size uint32) ([]byte, error) {
	data, ok := wm.instance.Memory().Read(offset, size)
	if !ok {
		return nil, fmt.Errorf("failed to read wasm memory at offset %d len %d", offset, size)
	}
	return data, nil
}

func (wm *WasmModule) readResult(results []uint64) (string, error) {
	if len(results) < 1 {
		return "", fmt.Errorf("no results from wasm export")
	}
	packed := results[0]
	ptr := uint32(packed >> 32)
	length := uint32(packed)
	if length == 0 {
		return "", nil
	}
	data, err := wm.readMemory(ptr, length)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (wm *WasmModule) GetManifest() (*Manifest, error) {
	results, err := wm.callExport("fasterp_get_manifest")
	if err != nil {
		return nil, err
	}
	jsonStr, err := wm.readResult(results)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal([]byte(jsonStr), &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}
	return &manifest, nil
}

// HandleRequest calls the Wasm module's fasterp_handle_request export.
// Returns (statusCode, responseBody, error).
func (wm *WasmModule) HandleRequest(method, path string, headers map[string]string, body string) (int, string, error) {
	if wm.getExport("fasterp_handle_request") == nil {
		return 0, "", nil
	}

	req := map[string]interface{}{
		"method":  method,
		"path":    path,
		"headers": headers,
		"body":    body,
	}
	reqData, _ := json.Marshal(req)

	ptr, err := wm.writeToMemory(reqData)
	if err != nil {
		return 0, "", fmt.Errorf("failed to write request: %w", err)
	}

	results, err := wm.callExport("fasterp_handle_request", uint64(ptr), uint64(len(reqData)))
	if err != nil {
		return 0, "", fmt.Errorf("fasterp_handle_request failed: %w", err)
	}

	respJSON, err := wm.readResult(results)
	if err != nil {
		return 0, "", fmt.Errorf("failed to read response: %w", err)
	}
	if respJSON == "" {
		return 200, "", nil
	}

	var resp struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers,omitempty"`
		Body    string            `json:"body"`
	}
	if err := json.Unmarshal([]byte(respJSON), &resp); err != nil {
		return 0, respJSON, nil
	}
	if resp.Status == 0 {
		resp.Status = 200
	}
	return resp.Status, resp.Body, nil
}

func (wm *WasmModule) Validate(model, record string) (string, error) {
	if wm.getExport("fasterp_validate") == nil {
		return "", nil
	}
	modelPtr, _ := wm.writeToMemory([]byte(model))
	recordPtr, _ := wm.writeToMemory([]byte(record))

	results, err := wm.callExport("fasterp_validate",
		uint64(modelPtr), uint64(len(model)),
		uint64(recordPtr), uint64(len(record)),
	)
	if err != nil {
		return "", err
	}
	return wm.readResult(results)
}

func (wm *WasmModule) callLifecycle(name string) error {
	if wm.getExport(name) == nil {
		return nil
	}
	_, err := wm.callExport(name)
	return err
}

func (wm *WasmModule) OnLoad() error  { return wm.callLifecycle("fasterp_on_load") }
func (wm *WasmModule) OnUnload() error { return wm.callLifecycle("fasterp_on_unload") }

// LoadWasmModule loads a .wasm file and returns a ModuleInstance + WasmModule.
func LoadWasmModule(path string) (*ModuleInstance, *WasmModule, error) {
	runtime := NewWasmRuntime()
	wasmMod, err := runtime.LoadModule(path)
	if err != nil {
		runtime.Close()
		return nil, nil, err
	}

	manifest, err := wasmMod.GetManifest()
	if err != nil {
		wasmMod.CloseAll()
		return nil, nil, fmt.Errorf("failed to get manifest from %s: %w", path, err)
	}

	log.Printf("[Wasm] Loaded module: %s v%s (%s)", manifest.Name, manifest.Version, manifest.Label)

	inst := &ModuleInstance{Manifest: manifest}

	for _, m := range manifest.Models {
		tableName := fmt.Sprintf("mod_%s_%s", manifest.Name, m.Name)
		sql := buildCreateTableSQL(tableName, m.Fields)
		inst.Models = append(inst.Models, ModelRegistration{
			Manifest:  m,
			TableName: tableName,
			SQL:       sql,
		})
	}

	for _, r := range manifest.Routes {
		inst.Routes = append(inst.Routes, Route{
			Method: r.Method,
			Path:   r.Path,
		})
	}

	inst.OnLoad = func() error { return wasmMod.OnLoad() }
	inst.OnUnload = func() error { return wasmMod.OnUnload() }

	return inst, wasmMod, nil
}

func buildCreateTableSQL(tableName string, fields []FieldDef) string {
	var cols []string
	cols = append(cols, "id UUID PRIMARY KEY DEFAULT gen_random_uuid()")
	cols = append(cols, "tenant_id UUID NOT NULL")
	for _, f := range fields {
		if !safeIdentRe.MatchString(f.Name) {
			continue
		}
		colType := fieldTypeToSQL(f.Type)
		null := ""
		if f.Required {
			null = " NOT NULL"
		}
		def := ""
		if f.Default != "" {
			def = " DEFAULT " + f.Default
		}
		cols = append(cols, fmt.Sprintf("  %s %s%s%s", f.Name, colType, null, def))
	}
	cols = append(cols, "created_at TIMESTAMP DEFAULT NOW()")
	cols = append(cols, "updated_at TIMESTAMP DEFAULT NOW()")
	return fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (\n%s\n)", tableName, strings.Join(cols, ",\n"))
}

func fieldTypeToSQL(t string) string {
	switch t {
	case "string":
		return "VARCHAR(255)"
	case "text":
		return "TEXT"
	case "int", "integer":
		return "INTEGER"
	case "float":
		return "FLOAT"
	case "bool", "boolean":
		return "BOOLEAN"
	case "date":
		return "DATE"
	case "datetime":
		return "TIMESTAMP"
	default:
		return "VARCHAR(255)"
	}
}
