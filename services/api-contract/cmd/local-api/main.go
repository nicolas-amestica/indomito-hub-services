package main

import (
	"context"
	"os"

	"ind-hub-api-gox-sls-pri-gh/services/api-contract/functions"
)

func main() {
	if err := functions.RunLocal(context.Background(), functions.RegisterCreate, functions.RegisterList, functions.RegisterGet, functions.RegisterUpdate, functions.RegisterPDF); err != nil {
		os.Exit(1)
	}
}
