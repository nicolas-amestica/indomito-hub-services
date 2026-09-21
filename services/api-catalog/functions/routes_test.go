package functions

import (
	"os"
	"regexp"
	"testing"
)

// serverlessConfigPath es la configuracion de despliegue del servicio, relativa
// a este paquete.
const serverlessConfigPath = "../serverless.ts"

// serverlessEndpointPattern captura el metodo y el path de cada entrada del
// arreglo `endpoints` de serverless.ts, en el orden en que aparecen.
var serverlessEndpointPattern = regexp.MustCompile(
	`method:\s*'([A-Z]+)',\s*\n\s*path:\s*'([^']+)',`,
)

// TestRoutesMatchServerlessConfig comprueba que las rutas declaradas en Go
// coincidan con los paths que serverless.ts despliega en el API Gateway
// compartido.
//
// Son dos declaraciones en dos lenguajes distintos y no hay forma de reducirlas
// a una sola: el README del repo advierte que es facil desincronizarlas y que
// el sintoma es un 404 que solo aparece en AWS, nunca en el servidor local.
// Este test convierte ese desajuste en un build roto.
func TestRoutesMatchServerlessConfig(t *testing.T) {
	source, err := os.ReadFile(serverlessConfigPath)
	if err != nil {
		t.Fatalf("no se pudo leer %s: %v", serverlessConfigPath, err)
	}

	matches := serverlessEndpointPattern.FindAllStringSubmatch(string(source), -1)

	declared := []Route{CatalogsRoute, ExchangeRatesRoute}
	if len(matches) != len(declared) {
		t.Fatalf(
			"serverless.ts declara %d endpoints y functions/routes.go declara %d",
			len(matches), len(declared),
		)
	}

	for index, want := range declared {
		got := Route{Method: matches[index][1], Path: matches[index][2]}
		if got != want {
			t.Errorf(
				"endpoint %d: serverless.ts declara %s %s y routes.go declara %s %s",
				index, got.Method, got.Path, want.Method, want.Path,
			)
		}
	}
}
