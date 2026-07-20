package sdk

import (
	"strings"
	"testing"
)

// ── Parser BPMN ──────────────────────────────────────────────────────────

// bpmnDoc arma un XML BPMN mínimo con el <process> dado, para no repetir el
// envoltorio <definitions> en cada test.
func bpmnDoc(process string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" id="defs" targetNamespace="http://fasterp">
  <process id="proc" isExecutable="true">
` + process + `
  </process>
</definitions>`
}

func TestParseBPMNDiagramaDeContactos(t *testing.T) {
	diagram := bpmnDoc(`
    <startEvent id="start" />
    <task id="t1" name="prospecto" />
    <task id="t2" name="activo" />
    <task id="t3" name="inactivo" />
    <endEvent id="end" />
    <sequenceFlow id="f0" sourceRef="start" targetRef="t1" />
    <sequenceFlow id="f1" sourceRef="t1" targetRef="t2" name="Activar" />
    <sequenceFlow id="f2" sourceRef="t3" targetRef="t2" name="Activar" />
    <sequenceFlow id="f3" sourceRef="t1" targetRef="t3" name="Desactivar" />
    <sequenceFlow id="f4" sourceRef="t2" targetRef="t3" name="Desactivar" />
`)

	parsed, err := ParseBPMNDiagram(diagram)
	if err != nil {
		t.Fatalf("diagrama válido rechazado: %v", err)
	}

	if parsed.Initial != "prospecto" {
		t.Errorf("initial = %q, want prospecto", parsed.Initial)
	}

	activar, ok := parsed.Transitions["activar"]
	if !ok {
		t.Fatal("falta la acción 'activar'")
	}
	if activar.To != "activo" {
		t.Errorf("activar.To = %q, want activo", activar.To)
	}
	wantFrom := map[string]bool{"prospecto": true, "inactivo": true}
	if len(activar.From) != 2 {
		t.Fatalf("activar.From = %v, want 2 orígenes", activar.From)
	}
	for _, f := range activar.From {
		if !wantFrom[f] {
			t.Errorf("origen inesperado en activar.From: %q", f)
		}
	}
}

// El flujo hacia el endEvent se ignora; el estado ya quedó registrado por
// otra transición, así que no hace falta que el flujo terminal aporte nada.
func TestParseBPMNFlujoHaciaFinSeIgnora(t *testing.T) {
	diagram := bpmnDoc(`
    <startEvent id="start" />
    <task id="t1" name="nuevo" />
    <task id="t2" name="listo" />
    <endEvent id="end" />
    <sequenceFlow id="f0" sourceRef="start" targetRef="t1" />
    <sequenceFlow id="f1" sourceRef="t1" targetRef="t2" name="avanzar" />
    <sequenceFlow id="f2" sourceRef="t2" targetRef="end" />
`)
	parsed, err := ParseBPMNDiagram(diagram)
	if err != nil {
		t.Fatalf("diagrama rechazado: %v", err)
	}
	if len(parsed.Transitions) != 1 {
		t.Errorf("transiciones = %v, want sólo 'avanzar'", parsed.Transitions)
	}
}

func TestParseBPMNSinEventoInicialFalla(t *testing.T) {
	diagram := bpmnDoc(`
    <task id="t1" name="nuevo" />
    <task id="t2" name="listo" />
    <sequenceFlow id="f1" sourceRef="t1" targetRef="t2" name="avanzar" />
`)
	if _, err := ParseBPMNDiagram(diagram); err == nil {
		t.Fatal("se esperaba error: no hay startEvent")
	}
}

func TestParseBPMNDosEventosInicialesFalla(t *testing.T) {
	diagram := bpmnDoc(`
    <startEvent id="start1" />
    <startEvent id="start2" />
    <task id="t1" name="nuevo" />
    <task id="t2" name="listo" />
    <sequenceFlow id="f0" sourceRef="start1" targetRef="t1" />
    <sequenceFlow id="f1" sourceRef="t1" targetRef="t2" name="avanzar" />
`)
	if _, err := ParseBPMNDiagram(diagram); err == nil {
		t.Fatal("se esperaba error: dos startEvent")
	}
}

func TestParseBPMNSinTransicionesEntreTareasFalla(t *testing.T) {
	diagram := bpmnDoc(`
    <startEvent id="start" />
    <task id="t1" name="nuevo" />
    <sequenceFlow id="f0" sourceRef="start" targetRef="t1" />
`)
	if _, err := ParseBPMNDiagram(diagram); err == nil {
		t.Fatal("se esperaba error: no hay transiciones entre tareas")
	}
}

func TestParseBPMNSinEtiquetaFalla(t *testing.T) {
	diagram := bpmnDoc(`
    <startEvent id="start" />
    <task id="t1" name="nuevo" />
    <task id="t2" name="listo" />
    <sequenceFlow id="f0" sourceRef="start" targetRef="t1" />
    <sequenceFlow id="f1" sourceRef="t1" targetRef="t2" />
`)
	if _, err := ParseBPMNDiagram(diagram); err == nil {
		t.Fatal("se esperaba error: flujo entre tareas sin nombre no tiene acción")
	}
}

func TestParseBPMNTareaSinNombreFalla(t *testing.T) {
	diagram := bpmnDoc(`
    <startEvent id="start" />
    <task id="t1" />
    <task id="t2" name="listo" />
    <sequenceFlow id="f0" sourceRef="start" targetRef="t1" />
    <sequenceFlow id="f1" sourceRef="t1" targetRef="t2" name="avanzar" />
`)
	if _, err := ParseBPMNDiagram(diagram); err == nil {
		t.Fatal("se esperaba error: tarea sin nombre no puede ser un estado")
	}
}

func TestParseBPMNMismaAccionDosDestinosFalla(t *testing.T) {
	diagram := bpmnDoc(`
    <startEvent id="start" />
    <task id="t1" name="nuevo" />
    <task id="t2" name="listo" />
    <task id="t3" name="cancelado" />
    <sequenceFlow id="f0" sourceRef="start" targetRef="t1" />
    <sequenceFlow id="f1" sourceRef="t1" targetRef="t2" name="avanzar" />
    <sequenceFlow id="f2" sourceRef="t1" targetRef="t3" name="avanzar" />
`)
	_, err := ParseBPMNDiagram(diagram)
	if err == nil {
		t.Fatal("se esperaba error: 'avanzar' no puede llevar a dos destinos distintos")
	}
}

func TestParseBPMNEtiquetasColisionanFalla(t *testing.T) {
	diagram := bpmnDoc(`
    <startEvent id="start" />
    <task id="t1" name="nuevo" />
    <task id="t2" name="listo" />
    <task id="t3" name="otro" />
    <sequenceFlow id="f0" sourceRef="start" targetRef="t1" />
    <sequenceFlow id="f1" sourceRef="t1" targetRef="t2" name="Avanzar Ya" />
    <sequenceFlow id="f2" sourceRef="t3" targetRef="t2" name="&quot;Avanzar Ya&quot;" />
`)
	_, err := ParseBPMNDiagram(diagram)
	if err == nil {
		t.Fatal("se esperaba error: etiquetas distintas que producen la misma acción")
	}
	if !strings.Contains(err.Error(), "misma acción") {
		t.Errorf("el error debería ser por colisión de etiqueta, fue: %v", err)
	}
}

func TestParseBPMNCompuertaExclusivaRechazada(t *testing.T) {
	diagram := bpmnDoc(`
    <startEvent id="start" />
    <task id="t1" name="nuevo" />
    <exclusiveGateway id="g1" name="¿Aprobado?" />
    <sequenceFlow id="f0" sourceRef="start" targetRef="t1" />
`)
	_, err := ParseBPMNDiagram(diagram)
	if err == nil {
		t.Fatal("se esperaba rechazo: compuertas no soportadas")
	}
	if !strings.Contains(err.Error(), "compuerta") {
		t.Errorf("el error debería mencionar la compuerta, fue: %v", err)
	}
}

func TestParseBPMNSubProcesoRechazado(t *testing.T) {
	diagram := bpmnDoc(`
    <startEvent id="start" />
    <task id="t1" name="nuevo" />
    <subProcess id="sp1" name="Revisión interna" />
    <sequenceFlow id="f0" sourceRef="start" targetRef="t1" />
`)
	_, err := ParseBPMNDiagram(diagram)
	if err == nil {
		t.Fatal("se esperaba rechazo: subprocesos no soportados")
	}
	if !strings.Contains(err.Error(), "subproceso") {
		t.Errorf("el error debería mencionar el subproceso, fue: %v", err)
	}
}

// El namespace BPMN no está fijado en los tags de struct, así que un
// documento con prefijo bpmn: (como el que exporta bpmn.io) tiene que
// parsear igual que uno con namespace por defecto.
func TestParseBPMNConPrefijoBpmn(t *testing.T) {
	diagram := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" id="defs">
  <bpmn:process id="proc" isExecutable="true">
    <bpmn:startEvent id="start" />
    <bpmn:task id="t1" name="nuevo" />
    <bpmn:task id="t2" name="listo" />
    <bpmn:sequenceFlow id="f0" sourceRef="start" targetRef="t1" />
    <bpmn:sequenceFlow id="f1" sourceRef="t1" targetRef="t2" name="avanzar" />
  </bpmn:process>
</bpmn:definitions>`

	parsed, err := ParseBPMNDiagram(diagram)
	if err != nil {
		t.Fatalf("diagrama con prefijo bpmn: rechazado: %v", err)
	}
	if parsed.Initial != "nuevo" {
		t.Errorf("initial = %q, want nuevo", parsed.Initial)
	}
	if _, ok := parsed.Transitions["avanzar"]; !ok {
		t.Fatal("falta la acción 'avanzar'")
	}
}

// Una colaboración con dos pools trae dos <process> hermanos bajo
// <definitions> — si el parser sólo mirara el primero (como antes de este
// chequeo), el segundo pool desaparecería en silencio. Tiene que rechazarse
// explícito, igual que una compuerta.
func TestParseBPMNColaboracionConDosPoolsRechazada(t *testing.T) {
	diagram := `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" id="defs">
  <collaboration id="collab">
    <participant id="part1" name="Ventas" processRef="proc1" />
    <participant id="part2" name="Depósito" processRef="proc2" />
  </collaboration>
  <process id="proc1" isExecutable="true">
    <startEvent id="start" />
    <task id="t1" name="nuevo" />
    <sequenceFlow id="f0" sourceRef="start" targetRef="t1" />
  </process>
  <process id="proc2" isExecutable="true">
    <startEvent id="start2" />
    <task id="t2" name="otro" />
    <sequenceFlow id="f1" sourceRef="start2" targetRef="t2" />
  </process>
</definitions>`

	_, err := ParseBPMNDiagram(diagram)
	if err == nil {
		t.Fatal("se esperaba rechazo: colaboración con más de un pool no soportada")
	}
	if !strings.Contains(err.Error(), "pool") {
		t.Errorf("el error debería mencionar los pools, fue: %v", err)
	}
}

// Un solo pool envuelto en una colaboración (algo que algunas herramientas
// hacen incluso con un único participante) tiene que seguir andando: no hay
// nada que se pierda en silencio acá.
func TestParseBPMNColaboracionConUnSoloPoolNoFalla(t *testing.T) {
	diagram := `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" id="defs">
  <collaboration id="collab">
    <participant id="part1" name="Ventas" processRef="proc1" />
  </collaboration>
  <process id="proc1" isExecutable="true">
    <startEvent id="start" />
    <task id="t1" name="nuevo" />
    <task id="t2" name="listo" />
    <sequenceFlow id="f0" sourceRef="start" targetRef="t1" />
    <sequenceFlow id="f1" sourceRef="t1" targetRef="t2" name="avanzar" />
  </process>
</definitions>`

	parsed, err := ParseBPMNDiagram(diagram)
	if err != nil {
		t.Fatalf("un solo pool no debería rechazarse: %v", err)
	}
	if parsed.Initial != "nuevo" {
		t.Errorf("initial = %q, want nuevo", parsed.Initial)
	}
}

// El resultado del parser tiene que poder colgarse tal cual de un manifest y
// pasar la MISMA validación que ya protege a los flujos escritos a mano — no
// hay un camino separado y más laxo para lo generado (mismo criterio que ya
// prueba TestParseMermaidProduceUnWorkflowValidoParaElMotor).
func TestParseBPMNProduceUnWorkflowValidoParaElMotor(t *testing.T) {
	diagram := bpmnDoc(`
    <startEvent id="start" />
    <task id="t1" name="prospecto" />
    <task id="t2" name="activo" />
    <task id="t3" name="inactivo" />
    <sequenceFlow id="f0" sourceRef="start" targetRef="t1" />
    <sequenceFlow id="f1" sourceRef="t1" targetRef="t2" name="Activar" />
    <sequenceFlow id="f2" sourceRef="t2" targetRef="t3" name="Desactivar" />
    <sequenceFlow id="f3" sourceRef="t3" targetRef="t2" name="Activar" />
`)
	parsed, err := ParseBPMNDiagram(diagram)
	if err != nil {
		t.Fatalf("parser rechazó un diagrama válido: %v", err)
	}

	manifest := &Manifest{
		Name: "demo",
		Models: map[string]*ModelDef{
			"item": {
				Fields: map[string]*FieldDef{
					"estado": {Type: "string", Options: parsed.States},
				},
				Workflow: &WorkflowDef{
					Field:       "estado",
					Initial:     parsed.Initial,
					Transitions: parsed.Transitions,
				},
			},
		},
	}

	if err := validateWorkflow("item", manifest.Models["item"]); err != nil {
		t.Fatalf("el workflow generado por el parser no pasa validateWorkflow: %v", err)
	}
}
