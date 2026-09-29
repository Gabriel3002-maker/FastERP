package sdk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LoadManifests escanea el directorio de módulos y devuelve los manifiestos
// válidos, indexados por nombre de módulo.
//
// Es la fuente única para generar la documentación: lo que un módulo declara es
// exactamente lo que el motor expone como API.
func LoadManifests(modulesDir string) (map[string]*Manifest, error) {
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return nil, fmt.Errorf("leer directorio de módulos: %w", err)
	}

	manifests := make(map[string]*Manifest)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		data, err := os.ReadFile(filepath.Join(modulesDir, entry.Name(), "manifest.json"))
		if err != nil {
			continue // directorio sin manifest: no es un módulo
		}

		m := &Manifest{}
		if err := json.Unmarshal(data, m); err != nil {
			continue // manifest ilegible: se omite de la documentación
		}
		if len(m.Models) == 0 {
			continue // módulo sin modelos: no expone API de datos
		}
		manifests[entry.Name()] = m
	}
	return manifests, nil
}

// BuildOpenAPI genera la especificación OpenAPI 3.0 de TODOS los módulos.
//
// Ningún módulo escribe documentación: su manifest ya dice qué modelos tiene y
// con qué campos, así que el motor deriva rutas, DTOs y ejemplos de ahí.
func BuildOpenAPI(manifests map[string]*Manifest, serverURL string) map[string]any {
	paths := map[string]any{}
	schemas := map[string]any{
		"Error":    errorSchema(),
		"PageMeta": pageMetaSchema(),
	}

	modules := make([]string, 0, len(manifests))
	for name := range manifests {
		modules = append(modules, name)
	}
	sort.Strings(modules)

	var tags []any
	for _, moduleName := range modules {
		manifest := manifests[moduleName]
		tags = append(tags, map[string]any{
			"name":        moduleName,
			"description": fmt.Sprintf("Modelos expuestos por el módulo %s", moduleName),
		})

		models := make([]string, 0, len(manifest.Models))
		for name := range manifest.Models {
			models = append(models, name)
		}
		sort.Strings(models)

		for _, modelName := range models {
			model := manifest.Models[modelName]
			schemaName := fmt.Sprintf("%s_%s", moduleName, modelName)

			schemas[schemaName] = modelSchema(model, false)
			schemas[schemaName+"_input"] = modelSchema(model, true)

			collection := fmt.Sprintf("/api/%s/%s", moduleName, modelName)
			item := collection + "/{id}"

			paths[collection] = map[string]any{
				"get":  listOperation(moduleName, modelName, model, schemaName),
				"post": createOperation(moduleName, modelName, schemaName),
			}
			paths[item] = map[string]any{
				"get":    readOperation(moduleName, modelName, schemaName),
				"put":    updateOperation(moduleName, modelName, schemaName),
				"delete": deleteOperation(moduleName, modelName),
			}
		}
	}

	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title": "FastERP @fast API",
			"description": "API REST generada automáticamente desde el manifest.json de cada módulo. " +
				"Todo modelo declarado obtiene CRUD, paginación (10/20/50/100), búsqueda y filtros sin escribir código.",
			"version": "1.0.0",
		},
		"servers": []any{map[string]any{"url": serverURL}},
		"tags":    tags,
		"paths":   paths,
		// El tenant se declara como esquema de seguridad: Swagger UI lo pide una
		// sola vez en "Authorize" y lo aplica a todas las operaciones.
		"security": []any{map[string]any{"TenantID": []any{}}},
		"components": map[string]any{
			"schemas": schemas,
			"securitySchemes": map[string]any{
				"TenantID": map[string]any{
					"type":        "apiKey",
					"in":          "header",
					"name":        "X-Tenant-ID",
					"description": "UUID o slug del tenant (por ejemplo: default).",
				},
			},
		},
	}
}

// modelSchema traduce los campos del manifest a un schema OpenAPI.
// input=true describe el cuerpo de escritura (sin campos administrados por el core).
func modelSchema(model *ModelDef, input bool) map[string]any {
	props := map[string]any{}
	var required []string

	if !input {
		props["id"] = map[string]any{"type": "string", "format": "uuid", "readOnly": true}
		props["created_at"] = map[string]any{"type": "string", "format": "date-time", "readOnly": true}
		props["updated_at"] = map[string]any{"type": "string", "format": "date-time", "readOnly": true}
	}

	for _, name := range model.OrderedFields() {
		def := model.Fields[name]
		spec := def.Spec()

		prop := map[string]any{"type": spec.JSONType}
		if spec.Format != "" {
			prop["format"] = spec.Format
		}
		if def.Type == "many2one" {
			prop["format"] = "uuid"
			prop["x-relation"] = map[string]any{
				"module": def.RelatedModule,
				"model":  def.RelatedModel,
				"field":  def.RelatedField,
			}
		}
		if def.Type == "one2many" || def.Type == "many2many" {
			prop["items"] = map[string]any{"type": "string", "format": "uuid"}
			prop["x-relation"] = map[string]any{
				"module": def.RelatedModule,
				"model":  def.RelatedModel,
				"field":  def.RelatedField,
			}
		}
		if def.Length > 0 && spec.JSONType == "string" {
			prop["maxLength"] = def.Length
		}
		if len(def.Options) > 0 {
			prop["enum"] = def.Options
		}
		if desc := strings.TrimSpace(def.Help); desc != "" {
			prop["description"] = desc
		} else if def.Label != "" {
			prop["description"] = def.Label
		}
		if def.Default != nil {
			prop["default"] = def.Default
		}
		if def.Example != nil {
			prop["example"] = def.Example
		}
		if def.Placeholder != "" {
			prop["x-placeholder"] = def.Placeholder
		}
		if def.VisibleIf != "" {
			prop["x-visibleIf"] = def.VisibleIf
		}
		if def.RequiredIf != "" {
			prop["x-requiredIf"] = def.RequiredIf
		}
		if def.Readonly || def.Computed {
			prop["readOnly"] = true
		}

		props[name] = prop
		if def.Required {
			required = append(required, name)
		}
	}

	schema := map[string]any{"type": "object", "properties": props}
	if input && len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func listOperation(module, model string, def *ModelDef, schemaName string) map[string]any {
	params := []any{
		queryParam("page", "integer", "Página solicitada (base 1).", 1),
		pageSizeParam(),
		queryParam("search", "string", "Búsqueda libre sobre los campos de texto del modelo.", nil),
		orderByParam(def),
		queryParam("order_dir", "string", "Dirección del orden: asc o desc.", "desc"),
	}

	// Cada campo del manifest se puede filtrar. Sin sufijo es igualdad exacta;
	// con __contains, __gte, etc. se elige el operador.
	for _, name := range def.OrderedFields() {
		field := def.Fields[name]
		jsonType := field.Spec().JSONType

		params = append(params, map[string]any{
			"name":        name,
			"in":          "query",
			"required":    false,
			"description": fmt.Sprintf("Filtra por igualdad exacta en %s.", name),
			"schema":      map[string]any{"type": jsonType},
		})

		for _, op := range fieldOperators(field) {
			params = append(params, map[string]any{
				"name":        name + "__" + op,
				"in":          "query",
				"required":    false,
				"description": fmt.Sprintf("%s en %s.", operatorHelp[op], name),
				"schema":      map[string]any{"type": jsonType},
			})
		}
	}

	return map[string]any{
		"tags":        []string{module},
		"summary":     fmt.Sprintf("Lista %s paginado", model),
		"description": "@fast.list() — devuelve una página de resultados con total y total_pages.",
		"operationId": fmt.Sprintf("list_%s_%s", module, model),
		"parameters":  params,
		"responses": map[string]any{
			"200": jsonResponse("Página de resultados", map[string]any{
				"allOf": []any{
					ref("#/components/schemas/PageMeta"),
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"data": map[string]any{
								"type":  "array",
								"items": ref("#/components/schemas/" + schemaName),
							},
						},
					},
				},
			}),
			"400": errorResponse("Tenant o filtro inválido"),
		},
	}
}

func createOperation(module, model, schemaName string) map[string]any {
	return map[string]any{
		"tags":        []string{module},
		"summary":     fmt.Sprintf("Crea un %s", model),
		"description": "@fast.create() — valida los campos obligatorios contra el manifest.",
		"operationId": fmt.Sprintf("create_%s_%s", module, model),
		"requestBody": requestBody(schemaName + "_input"),
		"responses": map[string]any{
			"201": jsonResponse("Registro creado", map[string]any{
				"type":       "object",
				"properties": map[string]any{"id": map[string]any{"type": "string", "format": "uuid"}},
			}),
			"400": errorResponse("Falta un campo obligatorio o el JSON es inválido"),
		},
	}
}

func readOperation(module, model, schemaName string) map[string]any {
	return map[string]any{
		"tags":        []string{module},
		"summary":     fmt.Sprintf("Obtiene un %s por ID", model),
		"description": "@fast.read()",
		"operationId": fmt.Sprintf("read_%s_%s", module, model),
		"parameters":  []any{idParam()},
		"responses": map[string]any{
			"200": jsonResponse("Registro encontrado", ref("#/components/schemas/"+schemaName)),
			"404": errorResponse("Registro no encontrado"),
		},
	}
}

func updateOperation(module, model, schemaName string) map[string]any {
	return map[string]any{
		"tags":        []string{module},
		"summary":     fmt.Sprintf("Actualiza un %s", model),
		"description": "@fast.update() — actualización parcial: sólo los campos enviados.",
		"operationId": fmt.Sprintf("update_%s_%s", module, model),
		"parameters":  []any{idParam()},
		"requestBody": requestBody(schemaName + "_input"),
		"responses": map[string]any{
			"200": jsonResponse("Registro actualizado", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":      map[string]any{"type": "string", "format": "uuid"},
					"message": map[string]any{"type": "string"},
				},
			}),
			"400": errorResponse("Datos inválidos"),
		},
	}
}

func deleteOperation(module, model string) map[string]any {
	return map[string]any{
		"tags":        []string{module},
		"summary":     fmt.Sprintf("Elimina un %s", model),
		"description": "@fast.delete()",
		"operationId": fmt.Sprintf("delete_%s_%s", module, model),
		"parameters":  []any{idParam()},
		"responses": map[string]any{
			"200": jsonResponse("Registro eliminado", map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":      map[string]any{"type": "string", "format": "uuid"},
					"message": map[string]any{"type": "string"},
				},
			}),
			"404": errorResponse("Registro no encontrado"),
		},
	}
}

// --- helpers de construcción ---

func ref(target string) map[string]any {
	return map[string]any{"$ref": target}
}

func idParam() map[string]any {
	return map[string]any{
		"name":     "id",
		"in":       "path",
		"required": true,
		"schema":   map[string]any{"type": "string", "format": "uuid"},
	}
}

func queryParam(name, typ, description string, def any) map[string]any {
	schema := map[string]any{"type": typ}
	if def != nil {
		schema["default"] = def
	}
	return map[string]any{
		"name": name, "in": "query", "required": false,
		"description": description, "schema": schema,
	}
}

// pageSizeParam publica los únicos tamaños de página aceptados.
func pageSizeParam() map[string]any {
	sizes := make([]any, len(PageSizes))
	for i, size := range PageSizes {
		sizes[i] = size
	}
	return map[string]any{
		"name": "limit", "in": "query", "required": false,
		"description": "Registros por página. Cualquier otro valor cae al default.",
		"schema": map[string]any{
			"type": "integer", "enum": sizes, "default": DefaultPageSize,
		},
	}
}

func orderByParam(def *ModelDef) map[string]any {
	options := []any{"created_at", "updated_at"}
	for _, name := range def.OrderedFields() {
		options = append(options, name)
	}

	return map[string]any{
		"name": "order_by", "in": "query", "required": false,
		"description": "Campo por el que ordenar. Debe existir en el manifest.",
		"schema":      map[string]any{"type": "string", "enum": options, "default": "created_at"},
	}
}

func requestBody(schemaName string) map[string]any {
	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": ref("#/components/schemas/" + schemaName),
			},
		},
	}
}

func jsonResponse(description string, schema map[string]any) map[string]any {
	return map[string]any{
		"description": description,
		"content":     map[string]any{"application/json": map[string]any{"schema": schema}},
	}
}

func errorResponse(description string) map[string]any {
	return jsonResponse(description, ref("#/components/schemas/Error"))
}

func errorSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"error": map[string]any{"type": "string", "description": "Motivo del fallo."},
		},
	}
}

func pageMetaSchema() map[string]any {
	sizes := make([]any, len(PageSizes))
	for i, size := range PageSizes {
		sizes[i] = size
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"total":       map[string]any{"type": "integer", "description": "Registros que cumplen el filtro."},
			"page":        map[string]any{"type": "integer", "description": "Página devuelta (base 1)."},
			"limit":       map[string]any{"type": "integer", "description": "Tamaño de página aplicado."},
			"total_pages": map[string]any{"type": "integer"},
			"page_sizes": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "integer"},
				"description": "Tamaños de página que puede elegir el usuario.",
				"example":     sizes,
			},
		},
	}
}

// operatorHelp describe cada operador de filtro en la documentación.
var operatorHelp = map[string]string{
	"contains": "Contiene el texto (sin distinguir mayúsculas)",
	"starts":   "Empieza con el texto",
	"ne":       "Distinto de",
	"gt":       "Mayor que",
	"gte":      "Mayor o igual que",
	"lt":       "Menor que",
	"lte":      "Menor o igual que",
}

// fieldOperators devuelve los operadores que tienen sentido para el campo:
// los de texto para lo buscable, los de comparación para números y fechas.
func fieldOperators(field *FieldDef) []string {
	if field.IsSearchable() {
		return []string{"contains", "starts", "ne"}
	}
	switch field.Spec().JSONType {
	case "integer", "number":
		return []string{"gt", "gte", "lt", "lte", "ne"}
	case "string": // fechas y uuid
		return []string{"gt", "gte", "lt", "lte", "ne"}
	default:
		return []string{"ne"}
	}
}
