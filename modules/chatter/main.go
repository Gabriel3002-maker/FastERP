package main

import (
	"encoding/json"
	"unsafe"
)

var manifest = map[string]interface{}{
	"name": "chatter",
	"models": map[string]interface{}{
		"message": map[string]interface{}{
			"fields": map[string]interface{}{
				"body":         map[string]interface{}{"type": "text", "required": true},
				"message_type": map[string]interface{}{"type": "string", "required": true},
				"author_name":  map[string]interface{}{"type": "string"},
				"author_id":    map[string]interface{}{"type": "string"},
				"record_model": map[string]interface{}{"type": "string"},
				"record_id":    map[string]interface{}{"type": "string"},
				"parent_id":    map[string]interface{}{"type": "string"},
				"channel":      map[string]interface{}{"type": "string"},
				"mentions":     map[string]interface{}{"type": "json"},
			},
		},
		"notification": map[string]interface{}{
			"fields": map[string]interface{}{
				"user_id":      map[string]interface{}{"type": "string", "required": true},
				"user_name":    map[string]interface{}{"type": "string"},
				"type":         map[string]interface{}{"type": "string", "required": true},
				"message":      map[string]interface{}{"type": "text", "required": true},
				"message_id":   map[string]interface{}{"type": "string"},
				"record_model": map[string]interface{}{"type": "string"},
				"record_id":    map[string]interface{}{"type": "string"},
				"read":         map[string]interface{}{"type": "boolean"},
				"author_name":  map[string]interface{}{"type": "string"},
			},
		},
	},
}

var resultBuf [65536]byte

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
