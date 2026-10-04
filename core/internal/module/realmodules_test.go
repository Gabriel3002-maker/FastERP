package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// modulesDir busca el directorio de módulos del repo, saltando los tests cuando
// no se está ejecutando desde el árbol de fuentes.
func modulesDir(t *testing.T) string {
	t.Helper()
	// El test vive en core/internal/module, así que el repo está dos niveles
	// arriba de core/.
	for _, candidate := range []string{
		filepath.Join("..", "..", "..", "modules"),
		filepath.Join("..", "..", "modules"),
	} {
		if st, err := os.Stat(filepath.Join(candidate, "contacts", "manifest.json")); err == nil && !st.IsDir() {
			return candidate
		}
	}
	t.Skip("no se encuentra el directorio de módulos")
	return ""
}

// Estos son los módulos que de verdad hay. El loader tiene que leerlos tal cual:
// son los que están instalados, y un loader que solo entiende la forma nueva
// deja el producto sinCRM.
func TestCargaLosModulosQueHay(t *testing.T) {
	dir := modulesDir(t)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	var cargados int
	for _, e := range entries {
		if !e.IsDir() || e.Name()[0] == '.' {
			continue
		}
		path := FindManifest(dir, e.Name())
		if path == "" {
			continue
		}
		m, err := LoadManifest(path)
		if err != nil {
			t.Errorf("%s: %v", e.Name(), err)
			continue
		}
		if m.Name != e.Name() {
			t.Errorf("%s: el manifest declara name %q", e.Name(), m.Name)
			continue
		}
		cargados++
	}

	if cargados < 10 {
		t.Errorf("solo se cargaron %d módulos; se esperaban al menos 10", cargados)
	}
}

// El DDL de cada módulo real tiene que generarse sin error. Un tipo mal escrito
// en un manifest instalado es un fallo de arranque, y tiene que verse aquí, no
// en el primer cliente que intente guardar.
func TestGeneraDDLParaLosModulosQueHay(t *testing.T) {
	dir := modulesDir(t)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if !e.IsDir() || e.Name()[0] == '.' {
			continue
		}
		path := FindManifest(dir, e.Name())
		if path == "" {
			continue
		}
		m, err := LoadManifest(path)
		if err != nil {
			continue // ya reportado en el test anterior
		}
		if _, err := PlanSchema(m); err != nil {
			t.Errorf("%s: no se puede generar el esquema: %v", e.Name(), err)
		}
	}
}

// El orden de las columnas sale de sequence, no del orden de escritura.
//
// No es lo mismo: contacts declara name(10), estado(15), email(50)… y luego
// tax_id_type(20), que se escribieron en un momento distinto. Quien puso el 20
// quería que fuera el tercero, y el DDL tiene que respetarlo.
func TestElOrdenDeLosModulosRealesEsPorSequence(t *testing.T) {
	dir := modulesDir(t)
	path := FindManifest(dir, "contacts")
	if path == "" {
		t.Skip("no está el módulo contacts")
	}
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}

	contact := m.Models["contact"]
	if contact == nil {
		t.Fatal("contacts no declara el modelo contact")
	}
	if len(contact.Fields) == 0 {
		t.Fatal("contact no tiene campos")
	}

	orden := contact.orderedFields()
	if (orden[0].Name != "nombre" && orden[0].Name != "name") || orden[0].Sequence != 10 {
		t.Errorf("el primer campo es %s(%d), se esperaba nombre(10) o name(10)", orden[0].Name, orden[0].Sequence)
	}
	for i := 1; i < len(orden); i++ {
		if orden[i].Sequence < orden[i-1].Sequence {
			t.Errorf("orden incorrecto en la posición %d: %s(%d) antes que %s(%d)",
				i, orden[i-1].Name, orden[i-1].Sequence, orden[i].Name, orden[i].Sequence)
		}
	}

	// Y el DDL tiene que salir en ese mismo orden.
	sql, err := CreateTableSQL(m, contact)
	if err != nil {
		t.Fatalf("CreateTableSQL: %v", err)
	}
	prev := -1
	for _, f := range orden {
		i := indexOfColumn(sql, f.Name)
		if i < 0 {
			t.Fatalf("falta la columna %s en el DDL", f.Name)
		}
		if i < prev {
			t.Errorf("la columna %s sale antes que la anterior, pese a su sequence", f.Name)
		}
		prev = i
	}
}

// indexOfColumn busca la posición de una columna en el DDL generado.
func indexOfColumn(sql, name string) int {
	return strings.Index(sql, "\n  "+name+" ")
}

// Los defaults de los módulos reales tienen que salir cotizados y con el tipo
// correcto. "default": "borrador" es un texto, no SQL.
func TestLosDefaultsRealesSeDanComoDatos(t *testing.T) {
	dir := modulesDir(t)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if !e.IsDir() || e.Name()[0] == '.' {
			continue
		}
		path := FindManifest(dir, e.Name())
		if path == "" {
			continue
		}
		m, err := LoadManifest(path)
		if err != nil {
			continue
		}
		for _, name := range m.FieldOrderModel() {
			md := m.Models[name]
			if md == nil {
				continue
			}
			sql, err := CreateTableSQL(m, md)
			if err != nil {
				t.Errorf("%s.%s: %v", e.Name(), name, err)
				continue
			}
			if n := countTopLevelSemicolons(sql); n != 0 {
				t.Errorf("%s.%s: el DDL tiene %d sentencias extra", e.Name(), name, n)
			}
			// Un default de texto no puede acabar en una expresión suelta.
			for _, f := range md.orderedFields() {
				if f.Default == nil {
					continue
				}
				lit, ok := sqlLiteral(f.Default)
				if !ok {
					continue
				}
				if _, isText := f.Default.(string); isText {
					if len(lit) < 2 || lit[0] != '\'' {
						t.Errorf("%s.%s.%s: un default de texto debe ir cotizado, es %s", e.Name(), name, f.Name, lit)
					}
				}
			}
		}
	}
}
