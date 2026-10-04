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

// keyOrder devuelve, en el orden en que aparecen, las claves del objeto que
// sigue a "want:" en un manifest.
//
// Es un escaneo de bytes sobre el texto, no un segundo parseo: el objetivo es
// recuperar el orden, y hacerlo así es exacto para lo que escriben las personas
// (YAML e indentado, JSON en una línea oPretty). Solo mira claves de primer
// nivel del bloque que le sigue a la clave pedida, así que un "models:" dentro
// de otro objeto no confunde.
func keyOrder(data []byte, want string) []string {
	start := findBlockStart(data, want)
	if start < 0 {
		return nil
	}

	var keys []string
	depth := 0
	// El primer carácter del bloque ya es el principio de una clave.
	expectKey := true

	for i := start; i < len(data); i++ {
		c := data[i]

		if c == '"' || c == '\'' {
			quote := c
			j := i + 1
			for j < len(data) && data[j] != quote {
				if data[j] == '\\' {
					j++
				}
				j++
			}
			if j < len(data) && depth == 0 && expectKey {
				keys = append(keys, string(data[i+1:j]))
			}
			i = j
			expectKey = false
			continue
		}

		switch c {
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return keys // se acaba el bloque
			}
			depth--
		case '\n', '\r':
			expectKey = false
			continue
		case ' ', '\t':
			continue
		}

		if depth == 0 && expectKey {
			j := i
			for j < len(data) && data[j] != ':' && data[j] != '\n' && data[j] != '\r' {
				j++
			}
			if j < len(data) && data[j] == ':' {
				keys = append(keys, strings.TrimSpace(string(data[i:j])))
				i = j
				expectKey = false
				continue
			}
		}
		expectKey = c != ','
	}
	return keys
}

// findBlockStart localiza el índice justo después de "want:" y de su salto de
// línea, o -1 si no aparece como clave de primer nivel.
func findBlockStart(data []byte, want string) int {
	needle := []byte(want + ":")
	for i := 0; i+len(needle) <= len(data); i++ {
		// Solo en el borde de una línea, para no pillar un "models:" que sea
		// parte de otro valor.
		if i > 0 && data[i-1] != '\n' && data[i-1] != '\r' && data[i-1] != ' ' {
			continue
		}
		if string(data[i:i+len(needle)]) != string(needle) {
			continue
		}
		// Lo que precede debe ser el nombre de la clave o un "- " de YAML.
		j := i - 1
		for j >= 0 && (data[j] == ' ' || data[j] == '\t') {
			j--
		}
		if j < 0 || (data[j] != '\n' && data[j] != '\r' && data[j] != '-') {
			continue
		}
		k := i + len(needle)
		for k < len(data) && (data[k] == ' ' || data[k] == '\t' || data[k] == '\r' || data[k] == '\n') {
			k++
		}
		return k
	}
	return -1
}
