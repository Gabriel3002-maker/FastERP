package main

import (
	"encoding/json"
	"unsafe"
)

// Manifest del módulo — define schema para @fast CRUD
// Core lee esto → genera rutas /api/contacts/contact[/{id}]
// Frontend llama @fast.create, @fast.update, etc. automáticamente
var manifest = map[string]interface{}{
	"name": "contacts",
	"models": map[string]interface{}{
		"contact": map[string]interface{}{
			"fields": map[string]interface{}{
				"name":                   map[string]interface{}{"type": "string", "required": true},
				"email":                  map[string]interface{}{"type": "string", "required": false},
				"phone":                  map[string]interface{}{"type": "string", "required": false},
				"mobile":                 map[string]interface{}{"type": "string", "required": false},
				"company":                map[string]interface{}{"type": "string", "required": false},
				"job_title":              map[string]interface{}{"type": "string", "required": false},
				"tax_id_type":            map[string]interface{}{"type": "string", "required": false},
				"tax_id":                 map[string]interface{}{"type": "string", "required": false},
				"person_type":            map[string]interface{}{"type": "string", "required": false},
				"tax_regime":             map[string]interface{}{"type": "string", "required": false},
				"accounting_obligation":  map[string]interface{}{"type": "string", "required": false},
				"retention_agent":        map[string]interface{}{"type": "string", "required": false},
				"address":                map[string]interface{}{"type": "text", "required": false},
				"city":                   map[string]interface{}{"type": "string", "required": false},
				"province":               map[string]interface{}{"type": "string", "required": false},
				"country":                map[string]interface{}{"type": "string", "required": false},
				"postal_code":            map[string]interface{}{"type": "string", "required": false},
				"website":                map[string]interface{}{"type": "string", "required": false},
				"notes":                  map[string]interface{}{"type": "text", "required": false},
			},
		},
	},
}

var resultBuf [65536]byte

// Export manifest to core (uses @fast for CRUD)
//go:wasmexport fasterp_get_manifest
func fasterp_get_manifest() uint64 {
	data, _ := json.Marshal(manifest)
	n := copy(resultBuf[:], data)
	return uint64(uintptr(unsafe.Pointer(&resultBuf[0])))<<32 | uint64(n)
}

//go:wasmexport fasterp_on_load
func fasterp_on_load() uint32 { return 0 }

//go:wasmexport fasterp_on_unload
func fasterp_on_unload() uint32 { return 0 }

func main() {}
