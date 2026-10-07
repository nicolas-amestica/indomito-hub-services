// Command recover-receipts recupera una página de comprobantes DEV; simula salvo --apply.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"ind-hub-api-gox-sls-pri-gh/services/payment-receipts/functions"
)

func main() {
	date := flag.String("date", "", "fecha AAAA-MM-DD")
	limit := flag.Int("limit", 20, "máximo 100")
	cursor := flag.String("cursor", "", "ULID posterior")
	apply := flag.Bool("apply", false, "procesa y puede enviar correo")
	flag.Parse()
	result, err := functions.RecoverJobs(context.Background(), *date, int32(*limit), *cursor, *apply)
	if err != nil {
		fmt.Fprintln(os.Stderr, "No fue posible recuperar la página de comprobantes; revisa argumentos, permisos y configuración DEV.")
		os.Exit(1)
	}
	if err = json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, "No fue posible serializar el resultado.")
		os.Exit(1)
	}
	for _, item := range result.Results {
		if item.Status == "RETRY_FAILED" {
			os.Exit(1)
		}
	}
}
