package api

import (
	"testing"

	"github.com/fasterp/backend/internal/module"
)

func TestCheckValueIntegerAcotaA32Bits(t *testing.T) {
	f := module.FieldMeta{Name: "n", Type: "integer"}
	if err := checkValue(f, float64(3_000_000_000)); err == nil {
		t.Fatal("integer debería rechazar > int32")
	}
	if err := checkValue(f, float64(42)); err != nil {
		t.Fatalf("42 válido: %v", err)
	}
}

func TestCheckValueBigintAdmite60Bits(t *testing.T) {
	f := module.FieldMeta{Name: "n", Type: "bigint"}
	if err := checkValue(f, float64(4_000_000_000)); err != nil {
		t.Fatalf("bigint > int32 debe pasar: %v", err)
	}
}

func TestCheckValueManyToManyListaDeUUIDs(t *testing.T) {
	f := module.FieldMeta{Name: "tags", Type: "many2many"}
	if err := checkValue(f, "no-es-lista"); err == nil {
		t.Fatal("debe exigir lista")
	}
	if err := checkValue(f, []any{"123"}); err == nil {
		t.Fatal("debe exigir UUIDs")
	}
	if err := checkValue(f, []any{"550e8400-e29b-41d4-a716-446655440000"}); err != nil {
		t.Fatalf("UUID válido: %v", err)
	}
}

func TestInt64RangoEntero(t *testing.T) {
	if _, err := toInt64(float64(1.5)); err == nil {
		t.Fatal("1.5 no es entero")
	}
	if _, err := toInt64("42"); err == nil {
		t.Fatal("string no es un número en JSON")
	}
}
