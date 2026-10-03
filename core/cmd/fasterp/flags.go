package main

import (
	"flag"
)

// parseFlexible parsea flags y argumentos posicionales en cualquier orden.
//
// El paquete flag se detiene en el primer argumento que no empieza por "-", y
// devuelve el resto sin interpretar. Eso hace que "fasterp new clientes --label
// X" ignores --label en silencio, que es la peor forma de fallar: el comando
// dice que ha hecho una cosa y ha hecho otra.
//
// Aquí se reintenta el parseo con lo que quedó, así que el nombre puede ir
// delante o detrás de los flags.
func parseFlexible(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	rest := args

	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}
}
