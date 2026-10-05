package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var modNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// runNew crea un módulo.
//
// El andamiaje es un module.yaml con un modelo y un workflow, no un esqueleto
// vacío: alguien tiene que poder añadir un campo y ver la columna aparecer, y
// eso solo se entiende si el punto de partida ya trae un caso completo.
func runNew(args []string) error {
	fs := flag.NewFlagSet("new", flag.ExitOnError)
	label := fs.String("label", "", "Etiqueta legible del módulo")
	dirFlag := fs.String("dir", "", "Directorio donde crear el módulo (por defecto, dentro de modules/)")
	modelsDir := fs.String("modules-dir", modulesDirDefault(), "Directorio de módulos")
	// flag.Parse se detiene en el primer argumento que no empieza por "-", así
	// que "new clientes --dir /tmp/x" dejaría los flags sin leer. parseFlexible
	// los acepta en cualquier orden, que es como los escribe la gente.
	positional, err := parseFlexible(fs, args)
	if err != nil {
		return err
	}
	if len(positional) == 0 {
		return fmt.Errorf("hace falta el nombre del módulo: fasterp new clientes")
	}
	name := positional[0]

	if !modNameRe.MatchString(name) {
		return fmt.Errorf("nombre de módulo inválido %q: minúsculas, dígitos y guion bajo, empezando por letra", name)
	}

	labelText := *label
	if labelText == "" {
		labelText = titleize(name)
	}

	// modelsDir puede ser una lista ":"-separada: el nuevo módulo se crea en
	// la primera raíz, que es donde quien escribe el yaml va luego a editarla.
	primary := *modelsDir
	if i := strings.IndexByte(primary, ':'); i >= 0 {
		primary = strings.TrimSpace(primary[:i])
	}

	dir := filepath.Join(primary, name)
	if *dirFlag != "" {
		dir = *dirFlag
	}
	if st, err := os.Stat(dir); err == nil {
		if !st.IsDir() {
			return fmt.Errorf("%s existe y no es un directorio", dir)
		}
		if !isEmptyDir(dir) {
			return fmt.Errorf("%s ya existe y no está vacío", dir)
		}
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	manifest := scaffoldManifest(name, labelText)
	if err := os.WriteFile(filepath.Join(dir, "module.yaml"), []byte(manifest), 0644); err != nil {
		return err
	}

	readme := fmt.Sprintf(`# %s

Módulo de FastERP. Su esquema está en `+"`module.yaml`"+`; no hay SQL, ni
`+"`main.go`"+`, ni binario que compilar.

## Ciclo de desarrollo

1. Edita `+"`module.yaml`"+`.
2. `+"`fasterp dev`"+` lo recarga y añade las columnas nuevas.

La tabla, el CRUD, las vistas de lista, ficha, tarjeta y kanban, el menú y el
historial de estados salen del manifest. Para cambiar algo, se cambia el YAML.

## Modelos

| Modelo   | Tabla                 | Qué es                    |
|----------|-----------------------|---------------------------|
| %-8s | `+"`mod_%s_%s`"+`     | %s |

## Campos

Edita la lista `+"`fields`"+` del modelo. Añadir una línea añade una columna:

`+"`yaml"+`
      - name: telefono
        type: phone
        label: Teléfono
        sequence: 25
`+"`"+`

La `+"`sequence`"+` ordena columnas y formulario. Usa 10, 20, 30… y deja huecos:
así insertar un campo entre dos no obliga a renumerar los que ya estaban.

## Tipos

`+"`string`"+`, `+"`text`"+`, `+"`email`"+`, `+"`phone`"+`, `+"`url`"+`, `+"`integer`"+`, `+"`bigint`"+`,
`+"`decimal`"+`, `+"`float`"+`, `+"`boolean`"+`, `+"`date`"+`, `+"`datetime`"+`, `+"`json`"+`,
`+"`enum`"+`, `+"`uuid`"+`, `+"`many2one`"+`.

Un tipo mal escrito falla al arrancar, no en la primera escritura.

## Instalar en otro servidor

Empaqueta el directorio y súbelo en el Module Store:

`+"`bash"+`
cd %s && zip -r ../%s.zip . -x '*.md'
`+"`"+`
`, labelText, name, name, strings.ToLower(strings.ReplaceAll(labelText, " ", "_")), labelText, filepath.Dir(dir), name)

	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(readme), 0644); err != nil {
		return err
	}

	fmt.Printf("Módulo %s creado en %s\n\n", name, dir)
	fmt.Println("  module.yaml   el módulo entero: modelos, campos, vistas, workflow")
	fmt.Println("  README.md     cómo se desarrolla")
	fmt.Println()
	printNextSteps(dir)

	return nil
}

// nextSteps son las tres cosas que alguien hace después de fasterp new.
// Se rellena en runNew porque el último depende de dónde se creó el módulo.
func printNextSteps(dir string) {
	fmt.Println("Siguiente paso:")
	fmt.Println("  fasterp check            # valida el manifest sin tocar la base")
	fmt.Println("  fasterp dev              # lo carga y recarga al guardar")
	fmt.Println()
	fmt.Println("Y ahora, edita module.yaml: añade un campo y guárdalo.")
	fmt.Println("La tabla, el CRUD y las vistas se generan de ahí.")
	fmt.Printf("Para verlo: http://localhost:7071/%s\n", filepath.Base(dir))
}

// scaffoldManifest es el punto de partida. Trae un modelo con los campos que
// casi siempre se necesitan y un workflow, porque el workflow es la parte que
// más cuesta entender leyendo el YAML vacío.
func scaffoldManifest(name, label string) string {
	model := singular(name)
	return fmt.Sprintf(`# %s — %s
#
# Este archivo es el módulo. El core crea las tablas, el CRUD y las vistas a
# partir de aquí: no hay SQL, ni Go, ni nada que compilar.
#
# Para verlo funcionando:
#   fasterp dev
# y edita lo que quieras aquí debajo. Al guardar, la columna aparece.

name: %s
label: %s
version: 0.1.0
description: ""
author: ""

models:
  %s:
    label: %s
    fields:
      - name: nombre
        type: string
        label: Nombre
        required: true
        sequence: 10

      - name: estado
        type: enum
        label: Estado
        options: [borrador, activo, archivado]
        default: borrador
        sequence: 20

      - name: notas
        type: text
        label: Notas
        sequence: 90

    # El workflow convierte un campo de options en una máquina de estados. Con
    # esto declarado, "estado" ya no se puede cambiar con un PUT normal: solo
    # por una transición, y cada cambio queda en el historial.
    workflow:
      field: estado
      initial: borrador
      transitions:
        activar:
          from: [borrador]
          to: activo
          label: Activar
        archivar:
          from: [borrador, activo]
          to: archivado
          label: Archivar

menus:
  %s:
    label: %s
    icon: package
    seq: 100
`, label, name, name, label, model, titleize(model), name, label)
}

// singular baja la última letra de un nombre en plural, que es lo que pasa con
// los nombres de módulo que alguien elige: clientes → cliente, pedidos → pedido.
func singular(name string) string {
	switch {
	case strings.HasSuffix(name, "ces"), strings.HasSuffix(name, "ses"):
		return strings.TrimSuffix(name, "es")
	case strings.HasSuffix(name, "s"):
		return strings.TrimSuffix(name, "s")
	default:
		return name
	}
}

func titleize(s string) string {
	s = strings.ReplaceAll(s, "_", " ")
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func isEmptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			return false
		}
	}
	return true
}
