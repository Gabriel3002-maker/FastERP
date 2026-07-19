package main

import (
	"encoding/json"
	"unsafe"
)

type FieldDef struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
}

type ModelDef struct {
	Name   string     `json:"name"`
	Label  string     `json:"label"`
	Fields []FieldDef `json:"fields"`
}

type MenuDef struct {
	Label string `json:"label"`
	Icon  string `json:"icon"`
	Route string `json:"route"`
	Seq   int    `json:"seq"`
}

type Manifest struct {
	Name        string     `json:"name"`
	Version     string     `json:"version"`
	Label       string     `json:"label"`
	Description string     `json:"description,omitempty"`
	Author      string     `json:"author,omitempty"`
	Icon        string     `json:"icon,omitempty"`
	Models      []ModelDef `json:"models"`
	Menus       []MenuDef  `json:"menus"`
}

var manifest = Manifest{
	Name:        "hello",
	Version:     "1.0.0",
	Label:       "Hello",
	Description: "Hello World example module",
	Author:      "FastERP Team",
	Icon:        "globe",
	Models: []ModelDef{
		{
			Name:  "greeting",
			Label: "Greetings",
			Fields: []FieldDef{
				{Name: "message", Type: "string", Label: "Message", Required: true},
				{Name: "language", Type: "string", Label: "Language"},
			},
		},
	},
	Menus: []MenuDef{
		{Label: "Hello", Icon: "globe", Route: "/hello", Seq: 10},
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
