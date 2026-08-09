package main

import (
	"context"
	"log"

	"ind-hub-api-gox-sls-pri-gh/services/api-auth/functions"
	helloworldv1 "ind-hub-api-gox-sls-pri-gh/services/api-auth/functions/hello-world-v1"
)

// Punto de entrada del servidor local para desarrollo.
func main() {
	if err := functions.RunLocal(
		context.Background(),
		helloworldv1.Register,
	); err != nil {
		log.Fatal(err)
	}
}
