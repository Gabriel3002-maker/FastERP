package module

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Watcher recarga los módulos cuando cambia su manifest.
//
// Es lo que cierra el ciclo de desarrollo: escribir un campo en module.yaml y
// guardar tiene que bastar. Sin esto habría que reiniciar el servidor por cada
// campo, que es justo el bucle lento que el manifest en YAML viene a quitar.
//
// No usa fsnotify a propósito. Los eventos de cambio de fichero en Linux son
// poco fiables con editores que escriben un temporal y lo renombran, y en Docker
// sobre bind mounts en Linux no llegan en absoluto — que es donde va a correr
// esto. Un sondeo cada segundo es predecible en todas partes.
type Watcher struct {
	mgr      *ModuleManager
	Interval time.Duration

	// debounce agrupa los cambios. Guardar un YAML con un formateador lo escribe
	// en varias pasadas, y sin esto se intentaría cargar un fichero a medio
	// escribir, que es justo el error que más desconcierta.
	debounce time.Duration
}

// NewWatcher crea un watcher con los intervalos por defecto.
func NewWatcher(mgr *ModuleManager) *Watcher {
	return &Watcher{mgr: mgr, Interval: time.Second, debounce: 250 * time.Millisecond}
}

// Watch bloquea hasta que el contexto se cancela, recargando lo que cambie.
func (w *Watcher) Watch(ctx context.Context, modulesDir string) error {
	interval := w.Interval
	if interval <= 0 {
		interval = time.Second
	}
	debounce := w.debounce
	if debounce <= 0 {
		debounce = 250 * time.Millisecond
	}

	known := w.snapshot(modulesDir)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Los cambios pendientes se acumulan aquí y se aplican juntos cuando el
	// escritor termina, no en cada tick.
	pending := map[string]time.Time{}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case now := <-ticker.C:
			// Un solo snapshot por vuelta: el bucle de abajo recorre el
			// directorio, y hacerlo dos veces por tick no aporta nada.
			current := w.snapshot(modulesDir)

			for name, seen := range current {
				if known[name] != seen {
					// Módulo nuevo, o manifest modificado. Los dos casos se
					// tratan igual: se recarga.
					pending[name] = now.Add(debounce)
					known[name] = seen
				}
			}

			// Módulos que ya no están en el directorio: se avisa para que
			// reload los descargue, igual que recarga los que cambian.
			for name := range known {
				if _, still := current[name]; !still {
					delete(known, name)
					pending[name] = now.Add(debounce)
				}
			}

			for name, readyAt := range pending {
				if now.Before(readyAt) {
					continue
				}
				delete(pending, name)
				w.reload(ctx, name)
			}
		}
	}
}

// reload recarga un módulo y dice qué pasó.
//
// Un manifest inválido no es un fallo del watcher: el módulo que ya estaba
// cargado se queda, y el error se enseña. Guardar un YAML a medias no puede
// dejar el servidor sin el módulo.
func (w *Watcher) reload(ctx context.Context, name string) {
	if _, err := os.Stat(filepath.Join(w.mgr.ModulesDir, name)); err != nil {
		// El directorio se borró: se descarga el módulo.
		if err := w.mgr.Unload(name); err != nil {
			log.Printf("[Watcher] no se pudo descargar %s: %v", name, err)
			return
		}
		log.Printf("[Watcher] %s: directorio borrado, módulo descargado", name)
		return
	}

	antes := w.resumen(name)
	if err := w.mgr.Reload(ctx, name); err != nil {
		log.Printf("[Watcher] %s: %v", name, err)
		return
	}
	despues := w.resumen(name)
	if antes != despues {
		log.Printf("[Watcher] %s: %s → %s", name, antes, despues)
	}
}

// resumen describe el estado del módulo en una línea, para poder decir qué ha
// cambiado al recargar en lugar de limitarse a "recargado".
func (w *Watcher) resumen(name string) string {
	inst := Global.Get(name)
	if inst == nil {
		return "sin cargar"
	}
	cols := 0
	for _, m := range inst.Models {
		cols += len(m.Columns)
	}
	return strings.Join([]string{
		inst.Manifest.Label,
		itoa(len(inst.Models)) + " modelo(s)",
		itoa(cols) + " campo(s)",
	}, ", ")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// snapshot devuelve el estado de cada módulo del directorio.
//
// La clave es lo que se compara entre vueltas, así que lleva el mtime y el
// tamaño: si el editor reescribe el mismo contenido, la recarga es un no-op
// barato y no merece un log.
func (w *Watcher) snapshot(modulesDir string) map[string]string {
	out := make(map[string]string)
	entries, err := os.ReadDir(modulesDir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := FindManifest(modulesDir, e.Name())
		if path == "" {
			continue
		}
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		out[e.Name()] = st.ModTime().UTC().Format(time.RFC3339Nano) + ":" +
			itoa(int(st.Size()))
	}
	return out
}
