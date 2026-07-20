package sdk

import (
	"fmt"
	"strings"
)

// ParsedWorkflow es lo que se obtiene de leer un diagrama de estados: los
// estados en el orden en que aparecen (para poblar "options" del campo que va
// a llevar el estado) y el flujo ya armado, listo para colgar de un
// ModelDef.Workflow.
type ParsedWorkflow struct {
	States      []string                  `json:"states"`
	Initial     string                    `json:"initial"`
	Transitions map[string]*TransitionDef `json:"transitions"`
}

// ParseMermaidStateDiagram lee un diagrama de estados en formato Mermaid
// (stateDiagram-v2) y lo convierte en la misma estructura que ya entiende el
// motor. Esto es lo que hace cierto "diseñar el flujo con un diagrama": el
// resultado es exactamente lo que alguien escribiría a mano en manifest.json,
// sólo que llega desde un dibujo en vez de tipearlo.
//
// Soporta:
//
//	[*] --> estado         declara el estado inicial
//	A --> B : accion       una transición; la etiqueta es obligatoria y se
//	                       convierte en el nombre de la acción (slugificada)
//	A --> [*]              marca de fin, se ignora — el motor no modela "sin
//	                       estado": todo registro siempre tiene un valor
//	%% comentario          se ignora
//
// No soporta, y lo rechaza con un error en vez de ignorarlo en silencio:
//
//	estados compuestos { ... }
//	declaraciones "state X as Y"
//
// Cualquier otra línea que no calce con lo anterior se ignora — cubre las
// descripciones cosméticas de Mermaid ("estado : texto") sin necesidad de
// interpretarlas.
func ParseMermaidStateDiagram(source string) (*ParsedWorkflow, error) {
	lines := strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n")

	transitions := make(map[string]*TransitionDef)
	labelOf := make(map[string]string) // acción → etiqueta original, para detectar colisiones

	var states []string
	seen := make(map[string]bool)
	addState := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		states = append(states, name)
	}

	var initials []string
	initialSeen := make(map[string]bool)

	for i, raw := range lines {
		lineNo := i + 1
		line := strings.TrimSpace(raw)

		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "%%"):
			continue
		case strings.EqualFold(line, "stateDiagram-v2"), strings.EqualFold(line, "stateDiagram"):
			continue
		case strings.HasPrefix(line, "note"):
			continue
		case strings.ContainsAny(line, "{}"):
			return nil, fmt.Errorf("línea %d: estados compuestos (\"{ }\") no soportados todavía", lineNo)
		case strings.HasPrefix(line, "state "):
			return nil, fmt.Errorf(
				"línea %d: la sintaxis \"state ...\" no está soportada; usá transiciones directas (A --> B : acción)",
				lineNo)
		}

		if !strings.Contains(line, "-->") {
			continue // descripción cosmética de un estado, o algo que no reconocemos: se ignora
		}

		from, to, label, err := parseTransitionLine(line, lineNo)
		if err != nil {
			return nil, err
		}

		if from == "[*]" {
			if !initialSeen[to] {
				initialSeen[to] = true
				initials = append(initials, to)
			}
			addState(to)
			continue
		}
		if to == "[*]" {
			addState(from) // el estado existe igual, aunque esta línea sólo marque el fin
			continue
		}

		if label == "" {
			return nil, fmt.Errorf(
				"línea %d: falta la etiqueta de la transición %q → %q (así queda el nombre de la acción)",
				lineNo, from, to)
		}

		addState(from)
		addState(to)

		action := slugifyAction(label)
		if existing, ok := transitions[action]; ok {
			if existing.To != to {
				return nil, fmt.Errorf(
					"línea %d: la acción %q ya lleva de %q a %q; no puede llevar también a %q",
					lineNo, action, existing.From[0], existing.To, to)
			}
			if prevLabel := labelOf[action]; prevLabel != label {
				return nil, fmt.Errorf(
					"línea %d: la etiqueta %q y %q producen la misma acción %q; usá nombres que no se confundan",
					lineNo, prevLabel, label, action)
			}
			if !containsState(existing.From, from) {
				existing.From = append(existing.From, from)
			}
		} else {
			transitions[action] = &TransitionDef{From: []string{from}, To: to, Label: label}
			labelOf[action] = label
		}
	}

	if len(initials) == 0 {
		return nil, fmt.Errorf(
			"falta el estado inicial: agregá una línea \"[*] --> nombre_del_estado\"")
	}
	if len(initials) > 1 {
		return nil, fmt.Errorf(
			"hay más de un estado inicial declarado (%s); sólo puede haber uno",
			strings.Join(initials, ", "))
	}
	if len(transitions) == 0 {
		return nil, fmt.Errorf(
			"el diagrama no tiene transiciones — agregá líneas \"A --> B : acción\"")
	}

	return &ParsedWorkflow{States: states, Initial: initials[0], Transitions: transitions}, nil
}

// parseTransitionLine separa "A --> B : etiqueta" en sus tres partes.
// Usa cortes de texto en vez de una sola regex: una regex que intente resolver
// a la vez el separador "-->" y el ":" opcional de la etiqueta es ambigua
// cuando no hay espacios (A-->B:etiqueta) — separar en dos pasos no lo es.
func parseTransitionLine(line string, lineNo int) (from, to, label string, err error) {
	left, right, ok := strings.Cut(line, "-->")
	if !ok {
		return "", "", "", fmt.Errorf("línea %d: no se pudo interpretar %q", lineNo, line)
	}

	from = strings.TrimSpace(left)
	toPart, labelPart, hasLabel := strings.Cut(right, ":")
	to = strings.TrimSpace(toPart)
	if hasLabel {
		label = strings.TrimSpace(labelPart)
	}

	if from == "" || to == "" {
		return "", "", "", fmt.Errorf("línea %d: transición incompleta: %q", lineNo, line)
	}
	return from, to, label, nil
}

// slugifyAction convierte la etiqueta de una transición en un nombre de
// acción válido: lo que exige identRe en validateWorkflow() (letras
// minúsculas, dígitos y guion bajo, empezando con letra).
//
// No hace normalización Unicode (una tilde se vuelve separador, no la letra
// sin tilde) — es una limitación conocida, no un bug: acentuar bien requeriría
// una tabla de reemplazos que no vale la pena para la primera versión.
func slugifyAction(label string) string {
	var b strings.Builder
	prevSep := true // arranca en true para no dejar un "_" colgando al inicio

	for _, r := range strings.ToLower(label) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevSep = false
		default:
			if !prevSep {
				b.WriteByte('_')
				prevSep = true
			}
		}
	}

	slug := strings.TrimRight(b.String(), "_")
	if slug == "" {
		slug = "accion"
	}
	if slug[0] >= '0' && slug[0] <= '9' {
		slug = "t_" + slug
	}
	if len(slug) > 63 { // identRe: ^[a-z][a-z0-9_]{0,62}$
		slug = strings.TrimRight(slug[:63], "_")
	}
	return slug
}
