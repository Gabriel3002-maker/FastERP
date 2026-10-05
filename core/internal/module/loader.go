package module

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// manifestFileNames son los nombres que se aceptan para el manifest, por orden
// de preferencia. El YAML va primero porque es el que se escribe a mano: un
// workflow con tres transiciones son 8 líneas en YAML y 25 en JSON.
var manifestFileNames = []string{"module.yaml", "module.yml", "manifest.yaml", "manifest.json"}

// FindManifest devuelve la ruta del manifest de un módulo, o "" si no tiene.
//
// Se comprueban las extensiones conocidas y, dentro de cada una, el nombre
// canónico y el genérico: el core haconvivido con manifest.json y con
// module.yaml, y un módulo escrito hace un año tiene que seguir cargando.
func FindManifest(modulesDir, moduleName string) string {
	dir := filepath.Join(modulesDir, filepath.Base(moduleName))
	for _, name := range manifestFileNames {
		if p := filepath.Join(dir, name); fileExists(p) {
			return p
		}
	}
	// Módulos planos: modules/<nombre>/ sin subcarpeta no aplica, pero por si
	// alguien dejó el manifest suelto en la raíz de modules/.
	for _, name := range manifestFileNames {
		if p := filepath.Join(modulesDir, filepath.Base(moduleName)+"_"+name); fileExists(p) {
			return p
		}
	}
	return ""
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// LoadManifest lee y valida el manifest de un módulo.
//
// El orden de los campos y de los menús se recupera del texto del fichero, no
// del mapa que devuelve el parser: un mapa de Go no tiene orden, y ese orden es
// información — quien escribió el manifest puso el campo identificador primero
// por algo.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer el manifest: %w", err)
	}
	m, err := ParseManifest(data, path)
	if err != nil {
		return nil, err
	}
	return m, nil
}

// ParseManifest convierte el contenido de un manifest en un Manifest validado.
func ParseManifest(data []byte, source string) (*Manifest, error) {
	ext := strings.ToLower(filepath.Ext(source))
	byName := source
	if byName == "" {
		byName = "module.yaml"
	}

	var m Manifest
	switch ext {
	case ".json":
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("manifest JSON inválido: %w", err)
		}
	default:
		// yaml.v3 lee JSON como un subconjunto del YAML, así que un .json con
		// extensión equivocada se procesa igual.
		if err := yaml.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("manifest YAML inválido: %w", err)
		}
	}

	m.fieldOrder, m.menuOrder = keyOrder(data, "models"), keyOrder(data, "menus")

	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(source), err)
	}
	return &m, nil
}

// FieldOrder devuelve los nombres de campo de un modelo en el orden de
// declaración. Si el manifest vino de un mapa JSON, cae al orden del slice.
func (m *Manifest) FieldOrder(model string) []string {
	if def := m.Models[model]; def != nil && len(def.FieldOrder) > 0 {
		return def.FieldOrder
	}
	if def := m.Models[model]; def != nil {
		out := make([]string, len(def.Fields))
		for i, f := range def.Fields {
			out[i] = f.Name
		}
		return out
	}
	return nil
}

// MenuOrder devuelve los menús en el orden en que se declararon.
func (m *Manifest) MenuOrder() []string { return m.menuOrder }

// ModelOrder devuelve los nombres de modelo en el orden en que se declararon.
//
// Es lo que permite que el menú deducido salga en el orden que escribió quien hizo
// el módulo, y no en el que imponga el mapa. Con varios modelos y sin nombre
// legible, un menú alfabético obliga a recorrer la lista entera para encontrar uno.
func (m *Manifest) ModelOrder() []string { return m.fieldOrder }

// keyOrder devuelve las claves de primer nivel del objeto que aparece bajo la
// clave `want:`, en el orden en que están escritas en el manifesto.
//
// Se lee con yaml.v3 como árbol de nodos por una razón concreta: los mapas de
// Go no conservan el orden, así que la única fuente de verdad del orden es el
// texto fuente via yaml.Node. Un escaneo de bytes heurístico anterior creía
// distinguir los niveles por sangría o por {}/[], y en YAML puro (que usa
// sangría, no llaves) devolvía basura como "abel"/"ields" en vez de los
// nombres de modelo. Eso hacía que BuildInstance registrara solo un modelo
// por manifest: el primero era el único que coincidía en ModelOrder(), y el
// resto quedaba atascado en el campo "sin registro". yaml.Node lo resuelve
// tanto para YAML como para JSON (yaml.v3 parsea JSON como subconjunto).
func keyOrder(data []byte, want string) []string {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil
	}
	if len(doc.Content) == 0 {
		return nil
	}
	top := doc.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != want {
			continue
		}
		sub := top.Content[i+1]
		if sub.Kind != yaml.MappingNode {
			return nil
		}
		var out []string
		for j := 0; j+1 < len(sub.Content); j += 2 {
			out = append(out, sub.Content[j].Value)
		}
		return out
	}
	return nil
}
