package module

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fasterp/backend/internal/db"
)

// ModuleManager carga, instala y descarga módulos desde el disco.
//
// Un módulo es un manifest (module.yaml o manifest.json) más, si quiere, un
// frontend/ y assets/. No hay binario: el esquema vive en el manifest y el core
// genera el SQL, así que editar el manifest y recargar es todo el ciclo.
type ModuleManager struct {
	mu         sync.RWMutex
	ModulesDir string
	UploadDir  string
}

func NewManager(modulesDir, uploadDir string) *ModuleManager {
	os.MkdirAll(modulesDir, 0755)
	os.MkdirAll(uploadDir, 0755)
	return &ModuleManager{ModulesDir: modulesDir, UploadDir: uploadDir}
}

type ModulePackage struct {
	Manifest *Manifest
	// ManifestRaw son los bytes tal cual venían en el paquete. Se escriben en
	// el disco en vez de reserializar el Manifest porque un YAML se escribe
	// para que una persona lo lea: sus comentarios y su formato son parte del
	// módulo, y quien acaba de subirlo va a seguir editándolo.
	ManifestRaw []byte
	Frontend    []byte
	Checksum    string
	Extracted   []string
}

// Límites de un paquete de módulo. El tamaño comprimido solo lo acota quien
// sube el archivo; una entrada de 64 KiB puede descomprimir a gigabytes, y
// ReadAll sin tope se lleva el proceso por delante.
const (
	maxPackageEntries = 256
	maxPackageBytes   = 128 << 20 // 128 MiB descomprimidos, en total
	maxEntryBytes     = 32 << 20  // por entrada
)

// ExtractModulePackage descomprime un paquete de módulo en el directorio de
// módulos.
//
// La extracción va antes de aplicar el esquema a propósito: es la parte que
// toca el disco y la que un paquete manipulado puede usar para escribir fuera,
// así que es la que se defiende. Un módulo que no se puede descomcribir no
// llega a ejecutar una sentencia.
func (m *ModuleManager) ExtractModulePackage(zipPath string) (*ModulePackage, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("no se pudo abrir el zip: %w", err)
	}
	defer r.Close()

	if len(r.File) > maxPackageEntries {
		return nil, fmt.Errorf("el paquete tiene %d entradas, el máximo es %d", len(r.File), maxPackageEntries)
	}

	pkg := &ModulePackage{}
	hasher := sha256.New()

	// Budget global de descompresión. Se descuenta de lo que realmente se lee,
	// no de lo que el zip promete: es el valor comprimido el que miente.
	budget := int64(maxPackageBytes)

	for _, f := range r.File {
		if f.UncompressedSize64 > uint64(maxEntryBytes) {
			return nil, fmt.Errorf("entrada %q descomprime a %d bytes, el máximo por entrada es %d",
				f.Name, f.UncompressedSize64, maxEntryBytes)
		}

		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		// io.LimitReader con un byte de más: si la entrada supera el presupuesto
		// se detecta por tener n+1 bytes, en lugar de fiarse del tamaño declarado.
		data, err := io.ReadAll(io.LimitReader(rc, budget+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > budget {
			return nil, fmt.Errorf("el paquete descomprime a más de %d bytes en total", int64(maxPackageBytes))
		}
		budget -= int64(len(data))

		hasher.Write(data)

		name := filepath.Clean(f.Name)
		switch {
		case isManifestName(name):
			// El manifest se valida aquí, antes de escribir nada: un paquete con
			// un manifest inválido no debe dejar archivos en el disco.
			parsed, err := ParseManifest(data, name)
			if err != nil {
				return nil, fmt.Errorf("manifest inválido: %w", err)
			}
			pkg.Manifest = parsed
			pkg.ManifestRaw = data

		case strings.HasSuffix(name, ".so"), strings.HasSuffix(name, ".wasm"),
			strings.HasSuffix(name, ".dll"), strings.HasSuffix(name, ".dylib"):
			// Un módulo ya no es un binario. Un .so sería plugin.Open(), que
			// ejecuta código Go arbitrario en este proceso con los permisos del
			// servidor; un .wasm es el runtime viejo, que se va. Ninguno tiene un
			// caso legítimo, y ambos se rechazan antes de tocar el disco.
			return nil, fmt.Errorf("el paquete incluye un binario ejecutable (%s): un módulo es un manifest, no un binario", filepath.Base(name))

		case strings.HasPrefix(name, "frontend/"):
			if pkg.Frontend == nil {
				pkg.Frontend = data
			}
			if err := m.writeExtracted(name, data); err != nil {
				return nil, err
			}

		case strings.HasPrefix(name, "assets/"):
			if err := m.writeExtracted(name, data); err != nil {
				return nil, err
			}
		}
	}

	if pkg.Manifest == nil {
		return nil, fmt.Errorf("el paquete no incluye module.yaml ni manifest.json")
	}

	// El manifest se escribe al final, ya validado.
	if err := m.writeExtracted("module.yaml", pkg.ManifestRaw); err != nil {
		return nil, err
	}

	pkg.Checksum = fmt.Sprintf("%x", hasher.Sum(nil))
	return pkg, nil
}

func isManifestName(name string) bool {
	base := filepath.Base(name)
	switch base {
	case "module.yaml", "module.yml", "manifest.yaml", "manifest.json":
		return filepath.Dir(name) == "."
	default:
		return false
	}
}

// nameInPackage es el nombre con el que se guarda el manifest ya validado.
func (m *Manifest) nameInPackage() string { return "module.yaml" }

// writeExtracted escribe un archivo del paquete dentro del directorio de
// módulos, rechazando los nombres que escribirían fuera.
func (m *ModuleManager) writeExtracted(name string, data []byte) error {
	dest, err := m.safePath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	return os.WriteFile(dest, data, 0644)
}

// safePath resuelve un nombre de entrada del zip contra el directorio de módulos.
//
// filepath.Join limpia los "..", así que join("/dir", "../etc/passwd") da
// "/etc/passwd" sin más aviso. Por eso el resultado se comprueba contra la raíz
// en vez de confiar en el Join.
func (m *ModuleManager) safePath(name string) (string, error) {
	root, err := filepath.Abs(m.ModulesDir)
	if err != nil {
		return "", err
	}
	dest, err := filepath.Abs(filepath.Join(root, name))
	if err != nil {
		return "", err
	}
	if dest != root && !strings.HasPrefix(dest, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("el paquete intenta escribir fuera del directorio de módulos: %q", name)
	}
	return dest, nil
}

// LoadModule carga un módulo del disco y lo registra, sin tocar la base.
//
// Separar la carga del esquema es lo que hace posible el watcher: un manifest
// que cambia solo necesita volver a cargarse, y las columnas se añaden cuando
// toca reconciliar.
func (m *ModuleManager) LoadModule(name string) (*ModuleInstance, error) {
	path := FindManifest(m.ModulesDir, name)
	if path == "" {
		return nil, fmt.Errorf("no se encontró el manifest de %q en %s (se busca module.yaml o manifest.json)", name, m.ModulesDir)
	}

	manifest, err := LoadManifest(path)
	if err != nil {
		return nil, err
	}
	if manifest.Name != name {
		return nil, fmt.Errorf("el manifest de %q declara name: %q", name, manifest.Name)
	}

	return m.BuildInstance(manifest, path), nil
}

// BuildInstance convierte un manifest en una instancia registrada. No toca disco
// ni base de datos.
func (m *ModuleManager) BuildInstance(manifest *Manifest, path string) *ModuleInstance {
	inst := &ModuleInstance{Manifest: manifest, SourcePath: path}

	for _, modelName := range manifest.FieldOrderModel() {
		def := manifest.Models[modelName]
		if def == nil {
			continue
		}
		inst.Models = append(inst.Models, ModelRegistration{
			Manifest:  def,
			TableName: manifest.TableName(modelName),
			Columns:   manifest.FieldOrder(modelName),
		})
	}

	for _, r := range manifest.Routes {
		inst.Routes = append(inst.Routes, Route{Method: r.Method, Path: r.Path})
	}

	Global.Register(inst)
	return inst
}

// ReconcileModule lleva la base de datos a lo que dice el manifest.
//
// Es idempotente: se puede llamar en cada arranque y después de cada cambio, y
// solo hace trabajo cuando el manifest y la tabla discrepan. Eso es lo que
// permite que añadir un campo a module.yaml no requiera una migración escrita a
// mano.
func (m *ModuleManager) ReconcileModule(ctx context.Context, name string) error {
	inst, err := m.LoadModule(name)
	if err != nil {
		return err
	}
	return m.Apply(ctx, inst)
}

// Apply ejecuta el plan de esquema de un módulo ya cargado.
func (m *ModuleManager) Apply(ctx context.Context, inst *ModuleInstance) error {
	if err := ApplySchema(ctx, inst.Manifest, db.DB); err != nil {
		return err
	}
	log.Printf("[Modules] %s: %d modelo(s) listos (%s)", inst.Manifest.Name, len(inst.Models), inst.Manifest.Version)
	return nil
}

// Reload recarga un módulo desde el disco y reconcilia el esquema.
//
// Es la operación que llama el watcher. Si el manifest nuevo es inválido, el
// módulo que ya estaba cargado se queda como estaba: un error de sintaxis al
// guardar un YAML no puede dejar el servidor sin el módulo.
func (m *ModuleManager) Reload(ctx context.Context, name string) error {
	path := FindManifest(m.ModulesDir, name)
	if path == "" {
		return m.Unload(name)
	}

	manifest, err := LoadManifest(path)
	if err != nil {
		// Se avisa y se sigue con el módulo anterior en memoria.
		log.Printf("[Modules] %s: manifest inválido, se conserva la versión anterior: %v", name, err)
		return err
	}
	if manifest.Name != name {
		log.Printf("[Modules] %s: el manifest declara name %q, no se recarga", name, manifest.Name)
		return fmt.Errorf("el manifest de %q declara name: %q", name, manifest.Name)
	}

	inst := m.BuildInstance(manifest, path)
	if err := m.Apply(ctx, inst); err != nil {
		return err
	}
	log.Printf("[Modules] %s recargado", name)
	return nil
}

// LoadAll carga y aplica todos los módulos del directorio, en orden de
// dependencias.
//
// Un módulo cuyas dependencias no están instaladas no se carga, y se dice
// cuáles faltan: es un error de instalación, no un fallo de arranque, y se
// entiende mucho mejor nombrándolo.
func (m *ModuleManager) LoadAll(ctx context.Context) error {
	entries, err := os.ReadDir(m.ModulesDir)
	if err != nil {
		return err
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		names = append(names, e.Name())
	}

	loaded := make(map[string]bool, len(names))
	var errs []string

	for pass := 0; pass < len(names); pass++ {
		progress := false
		for _, name := range names {
			if loaded[name] {
				continue
			}
			path := FindManifest(m.ModulesDir, name)
			if path == "" {
				continue
			}
			// Se lee el manifest solo para mirar las dependencias.
			manifest, err := LoadManifest(path)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", name, err))
				loaded[name] = true
				continue
			}
			if !m.depsReady(manifest, loaded, name) {
				continue
			}
			inst := m.BuildInstance(manifest, path)
			if err := m.Apply(ctx, inst); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", name, err))
			}
			loaded[name] = true
			progress = true
		}
		if !progress {
			break
		}
	}

	// Los que no llegaron a cargar por dependencias circulares o ausentes.
	for _, name := range names {
		if loaded[name] {
			continue
		}
		path := FindManifest(m.ModulesDir, name)
		if path == "" {
			continue
		}
		if manifest, err := LoadManifest(path); err == nil {
			errs = append(errs, fmt.Sprintf("%s: faltan dependencias (%s)", name, strings.Join(manifest.Depends, ", ")))
		}
	}

	for _, e := range errs {
		log.Printf("[Modules] %s", e)
	}
	if len(errs) > 0 {
		return fmt.Errorf("%d módulo(s) no se pudieron cargar", len(errs))
	}
	return nil
}

func (m *ModuleManager) depsReady(manifest *Manifest, loaded map[string]bool, self string) bool {
	for _, dep := range manifest.Depends {
		if dep == self || !loaded[dep] {
			return false
		}
	}
	return true
}

// Unload descarga un módulo y borra sus tablas.
func (m *ModuleManager) Unload(name string) error {
	inst := Global.Get(name)
	if inst == nil {
		return nil
	}

	for _, model := range inst.Models {
		if !safeIdent(model.TableName) {
			return fmt.Errorf("nombre de tabla no seguro: %q", model.TableName)
		}
		// CASCADE se queda con los índices y políticas de la tabla; sin él, un
		// módulo desinstalado deja objetos que impiden volver a instalarlo.
		if _, err := db.DB.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s CASCADE", model.TableName)); err != nil {
			log.Printf("[Modules] no se pudo borrar la tabla %s: %v", model.TableName, err)
			continue
		}
		log.Printf("[Modules] tabla borrada: %s", model.TableName)
	}

	Global.Unregister(name)
	log.Printf("[Modules] módulo descargado: %s", name)
	return nil
}
