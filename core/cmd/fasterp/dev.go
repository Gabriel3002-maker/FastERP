package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/fasterp/backend/internal/config"
	"github.com/fasterp/backend/internal/db"
	"github.com/fasterp/backend/internal/module"
)

// modulesDirDefault resuelve el directorio de módulos.
//
// Se mira primero FASTERP_MODULES_DIR, luego ./modules y por último ../modules:
// fasterp se ejecuta desde core/ (que es donde se compila) y desde la raíz del
// repo, y en los dos casos los módulos están en el mismo sitio relativo.
func modulesDirDefault() string {
	if d := os.Getenv("FASTERP_MODULES_DIR"); d != "" {
		return d
	}
	for _, candidate := range []string{"modules", "../modules"} {
		if st, err := os.Stat(candidate); err == nil && st.IsDir() {
			return candidate
		}
	}
	return "modules"
}

// runCheck valida los manifests y enseña qué esquema generarían.
//
// No necesita base de datos, y esa es la idea: el error de un YAML mal escrito
// tiene que salir antes de levantar Postgres, no después.
func runCheck(args []string) error {
	root := modulesDirDefault()
	var only string
	if len(args) > 0 {
		only = args[0]
	}

	var n int
	for _, dir := range splitModuleDirs(root) {
		if only != "" {
			if path := module.FindManifest(dir, only); path != "" {
				return checkOne(path, only)
			}
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return fmt.Errorf("no se pudo leer %s: %w", dir, err)
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			path := module.FindManifest(dir, e.Name())
			if path == "" {
				fmt.Printf("%-16s sin manifest\n", e.Name())
				continue
			}
			if err := checkOne(path, e.Name()); err != nil {
				return err
			}
			n++
		}
	}
	if only != "" {
		return fmt.Errorf("no se encontró el módulo %q", only)
	}
	if n == 0 {
		return fmt.Errorf("no hay módulos en %s", root)
	}
	return nil
}

// splitModuleDirs acepta "a:b" o "a" separados por ":".
func splitModuleDirs(root string) []string {
	var out []string
	for _, part := range strings.Split(root, ":") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{root}
	}
	return out
}

// checkOne valida el manifest de un módulo y enseña el esquema que generaría.
func checkOne(path, name string) error {
	m, err := module.LoadManifest(path)
	if err != nil {
		fmt.Printf("%-16s ✗ %v\n", name, err)
		return fmt.Errorf("%s: %v", name, err)
	}

	stmts, err := module.PlanSchema(m)
	if err != nil {
		fmt.Printf("%-16s ✗ %v\n", name, err)
		return fmt.Errorf("%s: %v", name, err)
	}

	fmt.Printf("%-16s ✓ %s — %d modelo(s), %d sentencia(s) de esquema\n",
		m.Name, m.Label, len(m.Models), len(stmts))
	for _, modelName := range m.FieldOrderModel() {
		def := m.Models[modelName]
		if def == nil {
			continue
		}
		cols := make([]string, 0, len(def.Fields))
		for _, f := range def.OrderedFields() {
			t := f.Type
			if len(f.Options) > 0 {
				t += "(" + strings.Join(f.Options, "|") + ")"
			}
			cols = append(cols, fmt.Sprintf("%s:%s", f.Name, t))
		}
		fmt.Printf("  %s  mod_%s_%s\n    %s\n", modelName, m.Name, modelName, strings.Join(cols, "  "))
		if def.Workflow != nil {
			for _, action := range sortedKeys(def.Workflow.Transitions) {
				tr := def.Workflow.Transitions[action]
				fmt.Printf("    %s: %s → %s (%s)\n", action, strings.Join(tr.From, ", "), tr.To, tr.Label)
			}
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// runDev carga los módulos contra la base y, en modo dev, se queda escuchando
// los cambios en los manifests.
//
// El watcher es la parte que hace que el ciclo sea corto. Guardar el YAML
// recarga el módulo y añade las columnas nuevas; no hace falta reiniciar, y no
// hace falta compilar nada.
func runDev(args []string) error {
	cfg := config.Load()

	if err := db.Connect(db.ConnConfig{
		DatabaseURL:  cfg.DatabaseURL,
		MaxOpenConns: cfg.MaxOpenConns,
		MaxIdleConns: cfg.MaxIdleConns,
	}); err != nil {
		return fmt.Errorf("no se pudo conectar a la base: %w", err)
	}
	defer db.Close()

	mgr := module.NewManager(modulesDirDefault(), cfg.UploadDir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	if err := mgr.LoadAll(ctx); err != nil {
		// No es fatal: un módulo roto no debería impedir trabajar sobre los
		// demás, y el error ya está en la salida.
		fmt.Fprintf(os.Stderr, "fasterp: %v\n", err)
	}

	fmt.Printf("%d módulo(s) cargados\n", len(module.Global.Names()))

	if !cfg.Dev {
		fmt.Println("fasterp dev está en modo carga. Usa FASTERP_DEV=true para esperar cambios.")
		return nil
	}

	fmt.Println("esperando cambios en los manifests… (Ctrl-C para salir)")

	w := module.NewWatcher(mgr)
	return w.Watch(ctx, modulesDirDefault())
}
