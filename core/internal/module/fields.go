package module

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"
)

// FieldsList son los campos de un modelo, y admite las dos formas en que se
// escriben en la práctica:
//
//	fields:                    fields:
//	  - name: nombre              nombre:
//	    type: string                type: string
//	  - name: email               email:
//	    type: string                type: string
//
// La lista es lo que se escribe a mano —el nombre va junto a su definición y se
// ve de un vistazo— y el mapa es lo que traen los manifest.json antiguos, donde
// la clave era el nombre.
//
// El orden se conserva en las dos formas. En la lista es trivial; en el mapa hay
// que recuperarlo, y por eso este tipo existe en vez de un []FieldDef a pelo:
// un mapa de Go no tiene orden, y ese orden es la disposición de la tabla y del
// formulario.
type FieldsList []FieldDef

// UnmarshalJSON acepta la forma de mapa y la de lista.
func (f *FieldsList) UnmarshalJSON(data []byte) error {
	// Mapa: {"nombre": {"type": "string"}, ...}
	var asMap map[string]FieldDef
	if err := json.Unmarshal(data, &asMap); err == nil {
		// El orden se recupera del texto, no del mapa: encoding/json no
		// garantiza el orden de Range, y aquí el orden es la disposición de la
		// tabla y del formulario.
		*f = fromMap(asMap, keyOrderJSON(data))
		return nil
	}
	// Lista: [{"name": "nombre", "type": "string"}, ...]
	var asList []FieldDef
	if err := json.Unmarshal(data, &asList); err != nil {
		return fmt.Errorf("fields debe ser una lista de campos o un mapa nombre: {…}: %w", err)
	}
	*f = asList
	return nil
}

// UnmarshalYAML acepta la forma de lista y, por cortesía, la de mapa.
func (f *FieldsList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.SequenceNode:
		var asList []FieldDef
		if err := node.Decode(&asList); err != nil {
			return err
		}
		*f = asList
		return nil
	case yaml.MappingNode:
		var asMap map[string]FieldDef
		if err := node.Decode(&asMap); err != nil {
			return err
		}
		// yaml.Node sí conserva el orden de un mapping, así que aquí no hace
		// falta el truco de ir a por los bytes.
		order := make([]string, 0, len(asMap))
		for i := 0; i+1 < len(node.Content); i += 2 {
			order = append(order, node.Content[i].Value)
		}
		*f = fromMap(asMap, order)
		return nil
	default:
		return fmt.Errorf("fields debe ser una lista o un mapa")
	}
}

// fromMap convierte un mapa nombre→definición en una lista ordenada.
func fromMap(asMap map[string]FieldDef, order []string) FieldsList {
	out := make(FieldsList, 0, len(asMap))
	seen := make(map[string]bool, len(asMap))

	add := func(name string, def FieldDef) {
		if def.Name == "" {
			def.Name = name
		}
		out = append(out, def)
		seen[name] = true
	}

	for _, name := range order {
		if def, ok := asMap[name]; ok {
			add(name, def)
		}
	}
	// Lo que no venga en el orden recuperado, en alfabético y sin repetir: el
	// orden tiene que ser estable entre arranques.
	var rest []string
	for name := range asMap {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	for _, name := range rest {
		add(name, asMap[name])
	}

	return out
}

// keyOrder devuelve las claves de un objeto JSON en el orden en que aparecen.
// Se usa para la forma de mapa, donde el orden es información.
func keyOrderJSON(data []byte) []string {
	var keys []string
	dec := json.NewDecoder(bytes.NewReader(data))
	// Se lee el primer objeto clave a clave: el decoder las devuelve en el orden
	// del documento, que es justo lo que encoding/json no garantiza al Range
	// sobre un mapa.
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil
	}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			break
		}
		key, ok := k.(string)
		if !ok {
			break
		}
		keys = append(keys, key)
		// Se salta el valor.
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			break
		}
	}
	return keys
}
