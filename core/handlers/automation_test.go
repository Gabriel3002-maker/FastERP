package handlers

import (
	"strings"
	"testing"
)

// execute() camina un grafo contra sdk.ModuleSDK real (Postgres) — no hay
// precedente en este repo de tests de Go que abran una conexión a la base
// (sdk_test.go y el resto de los tests son puros), así que ese camino se
// verifica en vivo contra el servidor de desarrollo, no acá. Lo que sí es
// lógica pura y vale la pena fijar con un test es evaluateCondition: es el
// único lugar donde una condición mal tipeada podría fallar en silencio en
// vez de decir explícitamente qué operador no entiende.
func TestEvaluateCondition(t *testing.T) {
	tests := []struct {
		name      string
		field     any
		operator  string
		target    any
		want      bool
		wantError bool
	}{
		{name: "eq verdadero", field: "activo", operator: "eq", target: "activo", want: true},
		{name: "eq falso", field: "activo", operator: "eq", target: "inactivo", want: false},
		{name: "neq verdadero", field: "activo", operator: "neq", target: "inactivo", want: true},
		{name: "neq falso", field: "activo", operator: "neq", target: "activo", want: false},
		{name: "compara por texto, no por tipo", field: 5, operator: "eq", target: "5", want: true},
		{name: "campo ausente (nil) vs literal", field: nil, operator: "eq", target: "activo", want: false},
		{name: "contains verdadero", field: "cliente@empresa.com", operator: "contains", target: "@empresa", want: true},
		{name: "contains falso", field: "cliente@empresa.com", operator: "contains", target: "@otra", want: false},
		{name: "gt verdadero", field: 120, operator: "gt", target: 100, want: true},
		{name: "gt falso", field: 80, operator: "gt", target: 100, want: false},
		{name: "lt verdadero", field: "80", operator: "lt", target: "100", want: true},
		{name: "gt con campo no numérico falla", field: "activo", operator: "gt", target: 100, wantError: true},
		{name: "gt con valor no numérico falla", field: 100, operator: "gt", target: "muchos", wantError: true},
		{name: "operador desconocido", field: "activo", operator: "regex", target: "a", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := evaluateCondition(tt.field, tt.operator, tt.target)
			if tt.wantError {
				if err == nil {
					t.Fatal("se esperaba error por operador desconocido")
				}
				return
			}
			if err != nil {
				t.Fatalf("no se esperaba error: %v", err)
			}
			if got != tt.want {
				t.Errorf("evaluateCondition(%v, %q, %v) = %v, want %v", tt.field, tt.operator, tt.target, got, tt.want)
			}
		})
	}
}

// resolveTemplate es la única puerta a "variables entre nodos" — igual que
// evaluateCondition, es lógica pura (no toca la base), así que se prueba
// acá; el hilado con stepOutputs dentro de execute() se prueba en vivo
// contra el servidor (ver nota arriba).
func TestResolveTemplate(t *testing.T) {
	ctx := map[string]map[string]any{
		"trigger": {"name": "María López", "estado": "activo"},
		"n1":      {"id": "abc-123", "email": "maria@empresa.com"},
	}

	tests := []struct {
		name      string
		raw       string
		want      string
		wantError bool
	}{
		{name: "sin tokens vuelve igual", raw: "texto fijo", want: "texto fijo"},
		{name: "un token del disparador", raw: "Hola {{trigger.name}}", want: "Hola María López"},
		{name: "un token de un nodo anterior", raw: "id: {{n1.id}}", want: "id: abc-123"},
		{name: "dos tokens en el mismo string", raw: "{{trigger.name}} <{{n1.email}}>", want: "María López <maria@empresa.com>"},
		{name: "espacios adentro de las llaves", raw: "{{ trigger.name }}", want: "María López"},
		{name: "origen desconocido falla con el token exacto", raw: "{{n99.campo}}", wantError: true},
		{name: "campo desconocido falla con el token exacto", raw: "{{trigger.telefono}}", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveTemplate(tt.raw, ctx)
			if tt.wantError {
				if err == nil {
					t.Fatalf("se esperaba error, dio %q", got)
				}
				if !strings.Contains(err.Error(), tt.raw) {
					t.Errorf("el error debería nombrar el token exacto %q, fue: %v", tt.raw, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("no se esperaba error: %v", err)
			}
			if got != tt.want {
				t.Errorf("resolveTemplate(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestResolveTemplateFields(t *testing.T) {
	ctx := map[string]map[string]any{"trigger": {"name": "Ana"}}

	resolved, err := resolveTemplateFields(map[string]any{
		"saludo":  "Hola {{trigger.name}}",
		"literal": "sin cambios",
		"numero":  42, // no-string: pasa igual, no hay nada que interpolar
	}, ctx)
	if err != nil {
		t.Fatalf("no se esperaba error: %v", err)
	}
	if resolved["saludo"] != "Hola Ana" {
		t.Errorf("saludo = %v, want %q", resolved["saludo"], "Hola Ana")
	}
	if resolved["literal"] != "sin cambios" {
		t.Errorf("literal = %v, want sin cambios", resolved["literal"])
	}
	if resolved["numero"] != 42 {
		t.Errorf("numero = %v, want 42 (no debería tocarse)", resolved["numero"])
	}
}
