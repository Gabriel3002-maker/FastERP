package sdk

import "sort"

// CatalogModule resume un módulo instalado: lo mínimo que un diseñador
// visual (Studio-Flujo u otro) necesita para ofrecer "extender esto" en vez
// de forzar siempre un módulo nuevo desde cero.
type CatalogModule struct {
	Name   string         `json:"name"`
	Label  string         `json:"label"`
	Icon   string         `json:"icon,omitempty"`
	Models []CatalogModel `json:"models"`
}

// CatalogModel resume un modelo: sus campos ya declarados (para no chocar
// nombres al agregar uno nuevo) y si ya tiene un flujo (para avisar que
// agregar otro lo reemplazaría en vez de coexistir).
type CatalogModel struct {
	Name        string         `json:"name"`
	Label       string         `json:"label"`
	Fields      []CatalogField `json:"fields"`
	HasWorkflow bool           `json:"has_workflow"`
}

// CatalogField es la versión mínima de un campo: sólo lo que hace falta para
// decidir si se puede agregar un campo con ese nombre o si ya existe.
type CatalogField struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Label    string   `json:"label"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required"`
}

// BuildCatalog convierte los manifiestos instalados en algo recorrible por un
// diseñador visual: qué módulos existen, qué modelos tiene cada uno, y qué
// campos ya están declarados en cada modelo.
//
// Es la misma fuente que ya alimenta la documentación (LoadManifests): un
// módulo entra al catálogo con las mismas reglas con que entra a la API.
func BuildCatalog(manifests map[string]*Manifest) []CatalogModule {
	moduleNames := make([]string, 0, len(manifests))
	for name := range manifests {
		moduleNames = append(moduleNames, name)
	}
	sort.Strings(moduleNames)

	catalog := make([]CatalogModule, 0, len(moduleNames))
	for _, moduleName := range moduleNames {
		manifest := manifests[moduleName]

		modelNames := make([]string, 0, len(manifest.Models))
		for name := range manifest.Models {
			modelNames = append(modelNames, name)
		}
		sort.Strings(modelNames)

		models := make([]CatalogModel, 0, len(modelNames))
		for _, modelName := range modelNames {
			model := manifest.Models[modelName]

			fields := make([]CatalogField, 0, len(model.Fields))
			for _, fieldName := range model.OrderedFields() {
				def := model.Fields[fieldName]
				fields = append(fields, CatalogField{
					Name:     fieldName,
					Type:     def.Type,
					Label:    labelFor(fieldName, def),
					Options:  def.Options,
					Required: def.Required,
				})
			}

			models = append(models, CatalogModel{
				Name:        modelName,
				Label:       labelForModel(modelName, model),
				Fields:      fields,
				HasWorkflow: model.Workflow != nil,
			})
		}

		catalog = append(catalog, CatalogModule{
			Name:   moduleName,
			Label:  moduleLabel(moduleName, manifest),
			Icon:   manifest.Icon,
			Models: models,
		})
	}
	return catalog
}

func moduleLabel(name string, m *Manifest) string {
	if m.Label != "" {
		return m.Label
	}
	return humanize(name)
}
