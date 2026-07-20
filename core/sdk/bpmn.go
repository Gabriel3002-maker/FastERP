package sdk

import (
	"encoding/xml"
	"fmt"
)

// ParseBPMNDiagram lee un diagrama BPMN 2.0 (el XML que exportan bpmn.io,
// Camunda Modeler, Bizagi, etc.) y lo convierte en la misma estructura que ya
// entiende el motor — exactamente lo que produce ParseMermaidStateDiagram
// para un diagrama de estados. Dos formas de dibujar el mismo flujo, un solo
// resultado.
//
// Los structs de abajo no fijan el namespace BPMN en sus tags ("startEvent",
// no "{http://.../MODEL} startEvent"): encoding/xml matchea por nombre local
// cuando el tag no trae namespace, así que da lo mismo qué prefijo use la
// herramienta de origen (bpmn:, default namespace, etc.).
//
// Soporta:
//
//	un solo <startEvent>
//	tareas (task, userTask, serviceTask, manualTask, scriptTask,
//	  businessRuleTask, sendTask, receiveTask) — cada una es un estado
//	<endEvent>                      terminal, se ignora
//	startEvent --sequenceFlow--> tarea      marca el estado inicial
//	tarea --sequenceFlow--> endEvent        se ignora, la tarea ya existe
//	tarea --sequenceFlow(name)--> tarea     transición; el name es obligatorio
//
// No soporta, y lo rechaza con un error en vez de interpretarlo mal:
//
//	gateways (exclusive, parallel, inclusive, event-based, complex)
//	subProcess / callActivity
//	boundaryEvent
//	cero o más de un startEvent
//	más de un pool/participante (colaboración) — ver comentario en
//	  ParseBPMNDiagram sobre por qué esto necesitaba chequeo aparte
func ParseBPMNDiagram(xmlSource string) (*ParsedWorkflow, error) {
	var defs bpmnDefinitions
	if err := xml.Unmarshal([]byte(xmlSource), &defs); err != nil {
		return nil, fmt.Errorf("no se pudo interpretar el XML de BPMN: %w", err)
	}

	// Un <definitions> con colaboración (varios pools) trae más de un
	// <process> como hermanos, uno por participante. Si sólo se leyera
	// Processes[0] como antes, el resto del diagrama se perdería en
	// silencio — justo lo que este parser evita en todos los demás casos
	// (por eso gateways, subprocesos, etc. se rechazan explícito en vez de
	// ignorarse). El chequeo de participantes es defensivo: cubre el caso
	// (raro pero posible con XML editado a mano) de una colaboración con un
	// único <process> pero dos <participant> apuntando a la misma.
	if len(defs.Processes) == 0 {
		return nil, fmt.Errorf("el XML no tiene ningún <process>")
	}
	if len(defs.Processes) > 1 {
		return nil, fmt.Errorf(
			"el diagrama tiene %d procesos (pools/participantes) — Studio-Flujo sólo soporta un proceso a la vez, sin pools",
			len(defs.Processes))
	}
	if defs.Collaboration != nil && len(defs.Collaboration.Participants) > 1 {
		return nil, fmt.Errorf(
			"el diagrama tiene %d participantes (pools) — Studio-Flujo sólo soporta un proceso a la vez, sin pools",
			len(defs.Collaboration.Participants))
	}
	proc := defs.Processes[0]

	if unsupported := firstUnsupported(proc); unsupported != "" {
		return nil, fmt.Errorf(
			"Studio-Flujo todavía no soporta %s — usá sólo eventos de inicio/fin, "+
				"tareas y flujos de secuencia con nombre", unsupported)
	}

	if len(proc.StartEvents) == 0 {
		return nil, fmt.Errorf("falta el evento de inicio (startEvent)")
	}
	if len(proc.StartEvents) > 1 {
		return nil, fmt.Errorf("hay más de un evento de inicio (startEvent); sólo puede haber uno")
	}
	startID := proc.StartEvents[0].ID

	endIDs := make(map[string]bool)
	for _, e := range proc.EndEvents {
		endIDs[e.ID] = true
	}

	labelOf := make(map[string]string) // id de tarea → nombre
	for _, t := range proc.activities() {
		if t.Name == "" {
			return nil, fmt.Errorf("la tarea %q no tiene nombre — hace falta para usarlo como estado", t.ID)
		}
		labelOf[t.ID] = t.Name
	}

	var states []string
	seen := make(map[string]bool)
	addState := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		states = append(states, name)
	}

	transitions := make(map[string]*TransitionDef)
	actionLabelOf := make(map[string]string) // acción → etiqueta original, para detectar colisiones

	var initial string

	for _, flow := range proc.SequenceFlows {
		switch {
		case flow.SourceRef == startID:
			to, ok := labelOf[flow.TargetRef]
			if !ok {
				return nil, fmt.Errorf(
					"el flujo %q sale del evento de inicio hacia %q, que no es una tarea conocida",
					flow.ID, flow.TargetRef)
			}
			initial = to
			addState(to)

		case endIDs[flow.TargetRef]:
			from, ok := labelOf[flow.SourceRef]
			if !ok {
				return nil, fmt.Errorf(
					"el flujo %q llega al evento de fin desde %q, que no es una tarea conocida",
					flow.ID, flow.SourceRef)
			}
			addState(from)

		default:
			from, ok := labelOf[flow.SourceRef]
			if !ok {
				return nil, fmt.Errorf("el flujo %q hace referencia a un origen desconocido: %q", flow.ID, flow.SourceRef)
			}
			to, ok := labelOf[flow.TargetRef]
			if !ok {
				return nil, fmt.Errorf("el flujo %q hace referencia a un destino desconocido: %q", flow.ID, flow.TargetRef)
			}
			if flow.Name == "" {
				return nil, fmt.Errorf(
					"falta el nombre del flujo %q → %q (así queda el nombre de la acción)", from, to)
			}

			addState(from)
			addState(to)

			action := slugifyAction(flow.Name)
			if existing, ok := transitions[action]; ok {
				if existing.To != to {
					return nil, fmt.Errorf(
						"la acción %q ya lleva de %q a %q; no puede llevar también a %q",
						action, existing.From[0], existing.To, to)
				}
				if prevLabel := actionLabelOf[action]; prevLabel != flow.Name {
					return nil, fmt.Errorf(
						"las etiquetas %q y %q producen la misma acción %q; usá nombres que no se confundan",
						prevLabel, flow.Name, action)
				}
				if !containsState(existing.From, from) {
					existing.From = append(existing.From, from)
				}
			} else {
				transitions[action] = &TransitionDef{From: []string{from}, To: to, Label: flow.Name}
				actionLabelOf[action] = flow.Name
			}
		}
	}

	if initial == "" {
		return nil, fmt.Errorf(
			"el evento de inicio no está conectado a ninguna tarea — agregá un flujo desde el startEvent")
	}
	if len(transitions) == 0 {
		return nil, fmt.Errorf("el diagrama no tiene transiciones entre tareas — agregá flujos con nombre")
	}

	return &ParsedWorkflow{States: states, Initial: initial, Transitions: transitions}, nil
}

// firstUnsupported devuelve la descripción del primer elemento que este
// subconjunto de BPMN no sabe interpretar, o "" si el proceso está limpio.
func firstUnsupported(p bpmnProcess) string {
	for _, g := range p.ExclusiveGateways {
		return fmt.Sprintf("la compuerta exclusiva %q", labelOrID(g))
	}
	for _, g := range p.ParallelGateways {
		return fmt.Sprintf("la compuerta paralela %q", labelOrID(g))
	}
	for _, g := range p.InclusiveGateways {
		return fmt.Sprintf("la compuerta inclusiva %q", labelOrID(g))
	}
	for _, g := range p.EventBasedGateways {
		return fmt.Sprintf("la compuerta basada en eventos %q", labelOrID(g))
	}
	for _, g := range p.ComplexGateways {
		return fmt.Sprintf("la compuerta compleja %q", labelOrID(g))
	}
	for _, s := range p.SubProcesses {
		return fmt.Sprintf("el subproceso %q", labelOrID(s))
	}
	for _, c := range p.CallActivities {
		return fmt.Sprintf("la actividad de llamada %q", labelOrID(c))
	}
	for _, b := range p.BoundaryEvents {
		return fmt.Sprintf("el evento de borde %q", labelOrID(b))
	}
	return ""
}

func labelOrID(n bpmnFlowNode) string {
	if n.Name != "" {
		return n.Name
	}
	return n.ID
}

// --- XML ---

type bpmnDefinitions struct {
	XMLName       xml.Name           `xml:"definitions"`
	Processes     []bpmnProcess      `xml:"process"`
	Collaboration *bpmnCollaboration `xml:"collaboration"`
}

type bpmnCollaboration struct {
	Participants []bpmnFlowNode `xml:"participant"`
}

type bpmnFlowNode struct {
	ID   string `xml:"id,attr"`
	Name string `xml:"name,attr"`
}

type bpmnSequenceFlow struct {
	ID        string `xml:"id,attr"`
	Name      string `xml:"name,attr"`
	SourceRef string `xml:"sourceRef,attr"`
	TargetRef string `xml:"targetRef,attr"`
}

type bpmnProcess struct {
	StartEvents []bpmnFlowNode `xml:"startEvent"`
	EndEvents   []bpmnFlowNode `xml:"endEvent"`

	Tasks             []bpmnFlowNode `xml:"task"`
	UserTasks         []bpmnFlowNode `xml:"userTask"`
	ServiceTasks      []bpmnFlowNode `xml:"serviceTask"`
	ManualTasks       []bpmnFlowNode `xml:"manualTask"`
	ScriptTasks       []bpmnFlowNode `xml:"scriptTask"`
	BusinessRuleTasks []bpmnFlowNode `xml:"businessRuleTask"`
	SendTasks         []bpmnFlowNode `xml:"sendTask"`
	ReceiveTasks      []bpmnFlowNode `xml:"receiveTask"`

	SequenceFlows []bpmnSequenceFlow `xml:"sequenceFlow"`

	ExclusiveGateways  []bpmnFlowNode `xml:"exclusiveGateway"`
	ParallelGateways   []bpmnFlowNode `xml:"parallelGateway"`
	InclusiveGateways  []bpmnFlowNode `xml:"inclusiveGateway"`
	EventBasedGateways []bpmnFlowNode `xml:"eventBasedGateway"`
	ComplexGateways    []bpmnFlowNode `xml:"complexGateway"`
	SubProcesses       []bpmnFlowNode `xml:"subProcess"`
	CallActivities     []bpmnFlowNode `xml:"callActivity"`
	BoundaryEvents     []bpmnFlowNode `xml:"boundaryEvent"`
}

// activities devuelve todos los elementos de tipo tarea del proceso, sin
// importar cuál de las variantes de BPMN sea — para este subconjunto todas
// se tratan igual: son un estado.
func (p bpmnProcess) activities() []bpmnFlowNode {
	var all []bpmnFlowNode
	all = append(all, p.Tasks...)
	all = append(all, p.UserTasks...)
	all = append(all, p.ServiceTasks...)
	all = append(all, p.ManualTasks...)
	all = append(all, p.ScriptTasks...)
	all = append(all, p.BusinessRuleTasks...)
	all = append(all, p.SendTasks...)
	all = append(all, p.ReceiveTasks...)
	return all
}
