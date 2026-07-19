package wasm

import (
	"encoding/json"
	"unsafe"
)

// FieldType represents the data type of a model field.
type FieldType string

const (
	FieldString FieldType = "string"
	FieldText   FieldType = "text"
	FieldInt    FieldType = "int"
	FieldFloat  FieldType = "float"
	FieldBool   FieldType = "bool"
	FieldDate   FieldType = "date"
	FieldDateTime FieldType = "datetime"
)

// FieldDef defines a field in a model.
type FieldDef struct {
	Name     string    `json:"name"`
	Type     FieldType `json:"type"`
	Label    string    `json:"label"`
	Required bool      `json:"required"`
	Default  string    `json:"default,omitempty"`
}

// ModelDef defines a model with its fields.
type ModelDef struct {
	Name   string     `json:"name"`
	Label  string     `json:"label"`
	Fields []FieldDef `json:"fields"`
}

// MenuDef defines a sidebar menu entry.
type MenuDef struct {
	Label string `json:"label"`
	Icon  string `json:"icon"`
	Route string `json:"route"`
	Seq   int    `json:"seq"`
}

// RouteDef defines a custom API route.
type RouteDef struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}

// Manifest is the module metadata (mirrors internal/module/manifest.go).
type Manifest struct {
	Name        string    `json:"name"`
	Version     string    `json:"version"`
	Label       string    `json:"label"`
	Description string    `json:"description,omitempty"`
	Author      string    `json:"author,omitempty"`
	Icon        string    `json:"icon,omitempty"`
	Depends     []string  `json:"depends,omitempty"`
	Models      []ModelDef `json:"models"`
	Menus       []MenuDef  `json:"menus"`
	Routes      []RouteDef `json:"routes,omitempty"`
}

// ModuleBuilder is the fluent builder for creating Wasm modules.
type ModuleBuilder struct {
	manifest Manifest
}

// NewModule creates a new module builder.
func NewModule(name, version, label string) *ModuleBuilder {
	return &ModuleBuilder{
		manifest: Manifest{
			Name:    name,
			Version: version,
			Label:   label,
		},
	}
}

// Description sets the module description.
func (m *ModuleBuilder) Description(d string) *ModuleBuilder {
	m.manifest.Description = d
	return m
}

// Author sets the module author.
func (m *ModuleBuilder) Author(a string) *ModuleBuilder {
	m.manifest.Author = a
	return m
}

// Icon sets the module icon.
func (m *ModuleBuilder) Icon(i string) *ModuleBuilder {
	m.manifest.Icon = i
	return m
}

// AddModel adds a model with fields to the module.
func (m *ModuleBuilder) AddModel(name, label string, fields ...FieldDef) *ModuleBuilder {
	m.manifest.Models = append(m.manifest.Models, ModelDef{
		Name:   name,
		Label:  label,
		Fields: fields,
	})
	return m
}

// AddMenu adds a sidebar menu entry.
func (m *ModuleBuilder) AddMenu(label, icon, route string, seq int) *ModuleBuilder {
	if route == "" {
		route = "/" + m.manifest.Name
	}
	m.manifest.Menus = append(m.manifest.Menus, MenuDef{
		Label: label,
		Icon:  icon,
		Route: route,
		Seq:   seq,
	})
	return m
}

// AddRoute adds a custom API route.
func (m *ModuleBuilder) AddRoute(method, path string) *ModuleBuilder {
	m.manifest.Routes = append(m.manifest.Routes, RouteDef{
		Method: method,
		Path:   path,
	})
	return m
}

// getManifestJSON serializes the manifest to JSON for the host.
func (m *ModuleBuilder) getManifestJSON() string {
	data, _ := json.Marshal(m.manifest)
	return string(data)
}

// Request represents an incoming HTTP request from the host.
type Request struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body"`
}

// Response represents an HTTP response to send back to the host.
type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body"`
}

// handleRequest is the user-overridable handler for custom routes.
var handleRequest func(req *Request) *Response

// SetHandler sets the custom route handler function.
func SetHandler(fn func(req *Request) *Response) {
	handleRequest = fn
}

// HandleRequest reads a request from Wasm linear memory, calls the registered
// handler, and writes the response to the memory buffer. Call from
// //go:wasmexport fasterp_handle_request in the module's main package.
func HandleRequest(ptr uint32, len uint32) uint64 {
	reqBytes := make([]byte, len)
	copy(reqBytes, unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), len))

	var req Request
	if err := json.Unmarshal(reqBytes, &req); err != nil {
		resp := &Response{Status: 400, Body: `{"error":"invalid request"}`}
		return writeResponse(resp)
	}

	resp := &Response{Status: 200, Body: ""}
	if handleRequest != nil {
		resp = handleRequest(&req)
	}
	if resp == nil {
		resp = &Response{Status: 404, Body: "not found"}
	}

	return writeResponse(resp)
}

func writeResponse(resp *Response) uint64 {
	data, _ := json.Marshal(resp)
	ensureBuffer(len(data))
	copy(memoryBuffer, data)
	addr := uintptr(unsafe.Pointer(&memoryBuffer[0]))
	return uint64(addr)<<32 | uint64(len(data))
}

// validate is the user-overridable validation function.
var validate func(model, record string) []string

// SetValidator sets the custom validation function.
func SetValidator(fn func(model, record string) []string) {
	validate = fn
}

// get returns the module builder reference.
// Module authors must set this in init().
var currentBuilder *ModuleBuilder

// SetModule sets the current module builder (call in init()).
func SetModule(mb *ModuleBuilder) {
	currentBuilder = mb
}

// Memory buffer helpers for writing/reading Wasm memory.
// These are used by the exported functions.

var memoryBuffer []byte
var memoryBufferLen int

const maxMemorySize = 1 << 20 // 1MB

func ensureBuffer(size int) {
	if memoryBufferLen < size {
		memoryBuffer = make([]byte, size)
		memoryBufferLen = size
	}
}
