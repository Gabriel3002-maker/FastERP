package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/fasterp/backend/internal/module"
)

func main() {
	sourceDir := flag.String("source", "", "Source directory of the module")
	outputFile := flag.String("output", "", "Output .zip file path (default: <name>.zip)")
	backendRoot := flag.String("backend", "", "Backend project root (default: current dir)")
	newModule := flag.String("new", "", "Create a new module scaffold with this name")
	label := flag.String("label", "", "Module label (for --new)")
	desc := flag.String("description", "", "Module description (for --new)")
	author := flag.String("author", "FastERP Team", "Module author (for --new)")
	flag.Parse()

	if *newModule != "" {
		if err := createScaffold(*newModule, *label, *desc, *author); err != nil {
			log.Fatalf("Failed to create module: %v", err)
		}
		return
	}

	if *sourceDir == "" {
		fmt.Println("Usage:")
		fmt.Println("  Create new module:  module-builder --new my_module --label \"My Module\" --description \"...\"")
		fmt.Println("  Build module:       module-builder --source ./my-module [--output my-module.zip] [--backend ../backend]")
		os.Exit(1)
	}

	root := *backendRoot
	if root == "" {
		root = "."
	}
	absRoot, _ := filepath.Abs(root)
	mgr := module.NewManager("./modules", "./uploads")
	if err := mgr.BuildModule(*sourceDir, *outputFile, absRoot); err != nil {
		log.Fatalf("Build failed: %v", err)
	}

	log.Printf("Module package created successfully!")
}

func createScaffold(name, label, description, author string) error {
	if label == "" {
		label = name
	}
	if description == "" {
		description = fmt.Sprintf("%s module for FastERP", label)
	}

	// Use basename for module name, full path for directory
	modName := filepath.Base(name)
	dir := name
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("directory %q already exists", dir)
	}

	os.MkdirAll(dir, 0755)
	os.MkdirAll(filepath.Join(dir, "frontend"), 0755)

	modelName := "record"
	modelLabel := label

	manifest := map[string]interface{}{
		"name":        modName,
		"version":     "1.0.0",
		"label":       label,
		"description": description,
		"author":      author,
		"icon":        "package",
		"depends":     []string{},
		"models": []map[string]interface{}{
			{
				"name":  modelName,
				"label": modelLabel,
				"fields": []map[string]interface{}{
					{"name": "name", "type": "string", "label": "Name", "required": true},
				},
			},
		},
		"menus": []map[string]interface{}{
			{"label": label, "icon": "PackageIcon", "route": "/" + modName + "/" + modelName, "seq": 100},
		},
	}
	manifestData, _ := json.MarshalIndent(manifest, "", "  ")
	os.WriteFile(filepath.Join(dir, "manifest.json"), manifestData, 0644)

	bt := "`"
	mainGo := fmt.Sprintf(`package main

import (
	wasmsdk "github.com/fasterp/backend/sdk/wasm"
)

func init() {
	wasmsdk.SetModule(wasmsdk.NewModule(%[10]q, "1.0.0", %[2]q).
		Description(%[3]q).
		Author(%[4]q).
		Icon("package").
		AddModel(%[5]q, %[6]q,
			wasmsdk.FieldDef{Name: "name", Type: wasmsdk.FieldString, Label: "Name", Required: true},
		).
		AddMenu(%[2]q, "PackageIcon", "/%[7]s/%[8]s", 100))
}

func main() {}
`, name, label, description, author, modelName, modelLabel, modName, modelName, bt, modName)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0644)

	pageName := toTitle(strings.ReplaceAll(name, "_", " "))
	frontendTSX := fmt.Sprintf(`import { useEffect, useState } from 'react'

export default function %sPage() {
  const [records, setRecords] = useState<any[]>([])
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    fetch('/api/%s/%s')
      .then(r => r.json())
      .then(setRecords)
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [])

  return (
    <div>
      <h1 className="text-2xl font-bold text-gray-900 mb-6">%s</h1>
      {loading ? (
        <div className="flex justify-center py-12">
          <div className="animate-spin h-8 w-8 border-4 border-blue-500 border-t-transparent rounded-full" />
        </div>
      ) : records.length === 0 ? (
        <div className="bg-white rounded-xl shadow-sm border border-gray-200 p-12 text-center">
          <p className="text-gray-500">No records yet.</p>
        </div>
      ) : (
        <div className="bg-white rounded-xl shadow-sm border border-gray-200 overflow-hidden">
          <table className="w-full">
            <thead>
              <tr className="border-b border-gray-200 bg-gray-50">
                {Object.keys(records[0]).filter(k => k !== 'id').map(key => (
                  <th key={key} className="text-left px-6 py-3 text-sm font-medium text-gray-500 capitalize">
                    {key.replace(/_/g, ' ')}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-200">
              {records.map((r: any) => (
                <tr key={r.id} className="hover:bg-gray-50">
                  {Object.entries(r).filter(([k]) => k !== 'id').map(([k, v]: [string, any]) => (
                    <td key={k} className="px-6 py-4 text-sm text-gray-900">{String(v)}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
`, pageName, modName, modelName, label)
	os.WriteFile(filepath.Join(dir, "frontend", "Page.tsx"), []byte(frontendTSX), 0644)

	log.Printf("Created module scaffold at: %s/", dir)
	log.Printf("  manifest.json  — edit models, menus, fields")
	log.Printf("  main.go        — Go Wasm module (compiles to .wasm with -buildmode=c-shared)")
	log.Printf("  frontend/Page.tsx — React page (uses auto-CRUD API)")
	log.Printf("\nNext steps:")
	log.Printf("  1. Edit main.go to add models, fields")
	log.Printf("  2. Run: module-builder --source %s", dir)
	log.Printf("  3. Upload the .zip in Module Store")
	return nil
}

func toTitle(s string) string {
	if s == "" {
		return ""
	}
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = string(w[0]-32) + w[1:]
		}
	}
	return strings.Join(words, " ")
}
