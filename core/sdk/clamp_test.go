package sdk

import "testing"

// ── ClampExternalLimit ────────────────────────────────────────────────────
//
// Es la línea que impide que un ?limit del cliente llegue tal cual al LIMIT de
// Postgres. Un fallo aquí no se ve: la consulta devuelve, el cliente recibe
// datos, y el proceso se muere por OOM un rato después. Por eso los casos
// redeciben el contrato entero, no solo el caso feliz.

func TestClampExternalLimitRespetaElTecho(t *testing.T) {
	casos := []struct {
		nombre          string
		limit, def, max int
		esperado        int
	}{
		{"dentro del rango", 500, 100, MaxExternalLimit, 500},
		{"justo en el techo", MaxExternalLimit, 100, MaxExternalLimit, MaxExternalLimit},
		{"por encima del techo se corta", 20000, 100, MaxExternalLimit, MaxExternalLimit},
		{"muy grande no desborda", 999999999, 100, MaxExternalLimit, MaxExternalLimit},
		{"cero usa el default", 0, 100, MaxExternalLimit, 100},
		{"negativo usa el default", -5, 100, MaxExternalLimit, 100},
		{"el default tambien pasa por el techo", 0, 10000, 1000, 1000},
		{"techo propio mas alto que el global", 20000, 10000, 10000, 10000},
		{"max cero cae al global", 5000, 100, 0, MaxExternalLimit},
		{"max negativo cae al global", 5000, 100, -1, MaxExternalLimit},
		{"default mayor que el max propio", 500, 5000, 1000, 500},
	}

	for _, c := range casos {
		t.Run(c.nombre, func(t *testing.T) {
			got := ClampExternalLimit(c.limit, c.def, c.max)
			if got != c.esperado {
				t.Errorf("ClampExternalLimit(%d, %d, %d) = %d, esperado %d",
					c.limit, c.def, c.max, got, c.esperado)
			}
		})
	}
}

// El default nunca puede superar el techo. Es la invariante que evita el fallo
// que existía antes de que max fuera un parámetro: el export pedía default
// 10000 con techo 1000, así que sin ?limit salían 10000 filas y con
// ?limit=20000 salían 1000 — pedir más devolvía menos que pedir nada.
func TestClampExternalLimitElDefaultNuncaExcedeElTecho(t *testing.T) {
	defs := []int{1, 50, 100, 1000, 9999, 10000, 50000, 1 << 30}
	maxs := []int{0, 1, 10, 100, 1000, 10000}

	for _, max := range maxs {
		for _, def := range defs {
			// El camino que devuelve def es limit <= 0.
			got := ClampExternalLimit(0, def, max)
			efectivo := max
			if efectivo <= 0 {
				efectivo = MaxExternalLimit
			}
			if got > efectivo {
				t.Errorf("ClampExternalLimit(0, %d, %d) = %d, que excede el techo %d",
					def, max, got, efectivo)
			}
		}
	}
}

// Ninguna combinación puede devolver 0 ni negativo: un LIMIT 0 en Postgres es
// un error de sintaxis, no un resultado vacío.
func TestClampExternalLimitNuncaDevuelveCeroNiNegativo(t *testing.T) {
	limits := []int{-1 << 30, -100, -1, 0, 1, 100, MaxExternalLimit, 1 << 30}
	defs := []int{-100, -1, 0, 1, 100, MaxExternalLimit}
	maxs := []int{-1, 0, 1, 10, MaxExternalLimit}

	for _, limit := range limits {
		for _, def := range defs {
			for _, max := range maxs {
				got := ClampExternalLimit(limit, def, max)
				if got < 1 {
					t.Errorf("ClampExternalLimit(%d, %d, %d) = %d, se esperaba al menos 1",
						limit, def, max, got)
				}
			}
		}
	}
}

// Pedir más nunca puede devolver menos. Es el fallo concreto que había en el
// export: con el techo global fijo en 1000 y el default en 10000, pedir
// ?limit=20000 devolvía 1000 filas mientras que no pedir nada devolvía 10000.
// El usuario que pide más se queda con menos, y no hay forma de que lo note
// salvo contando filas.
func TestClampExternalLimitPedirMasNuncaDevuelveMenos(t *testing.T) {
	// Es el caso del export de auditoría: default igual al techo propio, que es
	// como lo declara handlers/audit.go.
	const def, max = 10000, 10000

	sinParametro := ClampExternalLimit(0, def, max)
	if pedir := ClampExternalLimit(20000, def, max); pedir < sinParametro {
		t.Errorf("pedir 20000 devolvió %d, menos que las %d que devuelve no pedir nada",
			pedir, sinParametro)
	}
	if pedir := ClampExternalLimit(999999999, def, max); pedir < sinParametro {
		t.Errorf("pedir 999999999 devolvió %d, menos que las %d del default",
			pedir, sinParametro)
	}

	// Y en general: la función es monótona creciente en limit.
	prev := ClampExternalLimit(1, def, max)
	for _, limit := range []int{2, 10, 100, 999, 1000, 5000, 10000, 10001, 1 << 20} {
		got := ClampExternalLimit(limit, def, max)
		if got < prev {
			t.Errorf("no monótona: pedir %d devolvió %d, menos que pedir menos (%d)",
				limit, got, prev)
		}
		prev = got
	}
}
