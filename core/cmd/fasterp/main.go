// Command fasterp es la herramienta de desarrollo de módulos.
//
//	fasterp new <módulo>   crea un módulo nuevo con su module.yaml
//	fasterp dev            carga los módulos y recarga cuando cambia el manifest
//	fasterp check <módulo> valida un manifest sin arrancar nada
//
// La idea es que el ciclo sea: new, editar el YAML, guardar. No hay binario que
// compilar ni plugin que subir — el manifest es el módulo, y el core genera el
// esquema y la interfaz a partir de él.
package main

import (
	"fmt"
	"os"
)

const usage = `fasterp — herramientas de desarrollo de módulos

Uso:
  fasterp new <módulo> [--label <etiqueta>] [--dir <directorio>]
      Crea un módulo nuevo con su module.yaml, sus tablas y su menú.

  fasterp check [<módulo>]
      Valida los manifests y dice qué tablas generaría cada uno.

  fasterp dev
      Carga los módulos y los reconcilia. Si FASTERP_DEV=true, además espera
      los cambios en module.yaml y los recarga sin reiniciar.

Variables de entorno:
  FASTERP_MODULES_DIR   directorio de módulos (por defecto ./modules)
  FASTERP_DATABASE_URL  conexión a Postgres, necesaria para dev y check
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "new":
		err = runNew(os.Args[2:])
	case "check":
		err = runCheck(os.Args[2:])
	case "dev":
		err = runDev(os.Args[2:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "fasterp: subcomando desconocido %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "fasterp: %v\n", err)
		os.Exit(1)
	}
}
