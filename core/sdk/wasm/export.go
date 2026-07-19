package wasm

import (
	"encoding/json"
	"unsafe"
)

// Static buffer for returning strings to the host (64KB).
var resultBuf [65536]byte

//go:wasmexport fasterp_get_manifest
func fasterp_get_manifest() uint64 {
	data, _ := json.Marshal(currentBuilder.manifest)
	n := copy(resultBuf[:], data)
	ptr := uintptr(unsafe.Pointer(&resultBuf[0]))
	return uint64(ptr)<<32 | uint64(n)
}

//go:wasmexport fasterp_on_load
func fasterp_on_load() uint32 { return 0 }

//go:wasmexport fasterp_on_unload
func fasterp_on_unload() uint32 { return 0 }
