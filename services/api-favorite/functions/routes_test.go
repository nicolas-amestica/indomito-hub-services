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

// serverlessPublicFlagPattern captura el valor de `public` de cada endpoint.
//
// Esta anclado al inicio de linea con sangria para no confundirse con las
// menciones a `public` de los comentarios del archivo, que empiezan con `*`.
var serverlessPublicFlagPattern = regexp.MustCompile(
	`(?m)^\s+public:\s*(true|false),`,
)

// declaredRoutes son las cuatro rutas del servicio, en el mismo orden en que
// serverless.ts declara sus endpoints.
func declaredRoutes() []Route {
	return []Route{
		ListFavoritesRoute,
		CreateFavoriteRoute,
		UpdateFavoriteRoute,
		DeleteFavoriteRoute,
	}
}

// readServerlessConfig devuelve el contenido de serverless.ts.
func readServerlessConfig(t *testing.T) string {
	t.Helper()

	source, err := os.ReadFile(serverlessConfigPath)
	if err != nil {
		t.Fatalf("no se pudo leer %s: %v", serverlessConfigPath, err)
	}

	return string(source)
}

// TestRoutesMatchServerlessConfig comprueba que las rutas declaradas en Go
// coincidan con los paths que serverless.ts registraria en el API Gateway
// compartido.
//
// Son dos declaraciones en dos lenguajes distintos y no hay forma de reducirlas
// a una sola: el README del repo advierte que es facil desincronizarlas y que
// el sintoma es un 404 que solo aparece en AWS, nunca en el servidor local.
// Este test convierte ese desajuste en un build roto.
func TestRoutesMatchServerlessConfig(t *testing.T) {
	source := readServerlessConfig(t)

	matches := serverlessEndpointPattern.FindAllStringSubmatch(source, -1)

	declared := declaredRoutes()
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

// TestServerlessEndpointsRequireAuthorizer comprueba que los cuatro endpoints
// sigan declarados como protegidos.
//
// Es el Requirement 19.3 escrito como test. El scope de un favorito es la
// identidad de su dueno, asi que un `public: true` aca no seria un ajuste de
// configuracion: expondria los favoritos de todos los usuarios a cualquiera que
// conozca la URL. La consecuencia deliberada es que el servicio no se despliega
// mientras no exista el authorizer (19.4), y ese bloqueo lo impone el builder de
// common/go-service.ts al evaluar este archivo.
func TestServerlessEndpointsRequireAuthorizer(t *testing.T) {
	source := readServerlessConfig(t)

	matches := serverlessPublicFlagPattern.FindAllStringSubmatch(source, -1)

	if len(matches) != len(declaredRoutes()) {
		t.Fatalf(
			"serverless.ts declara %d banderas `public` y el servicio tiene %d endpoints",
			len(matches), len(declaredRoutes()),
		)
	}

	for index, match := range matches {
		if match[1] != "false" {
			t.Errorf(
				"endpoint %d declara public: %s — los favoritos dependen de la identidad del usuario y no pueden ser publicos (Requirement 19.3)",
				index, match[1],
			)
		}
	}
}

// TestLocalPathUsesEchoNotation comprueba la traduccion del path del API
// Gateway a la de Echo.
//
// Es la unica diferencia admitida entre lo que se despliega y lo que registra el
// servidor local, y esta acotada a la notacion del parametro: el nombre se
// conserva, para que el handler lo lea igual en los dos entornos.
func TestLocalPathUsesEchoNotation(t *testing.T) {
	cases := []struct {
		name  string
		route Route
		want  string
	}{
		{name: "sin parametros", route: ListFavoritesRoute, want: "/favoritos"},
		{name: "un parametro", route: UpdateFavoriteRoute, want: "/favoritos/:" + FavoriteIDParam},
		{name: "un parametro en delete", route: DeleteFavoriteRoute, want: "/favoritos/:" + FavoriteIDParam},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.route.LocalPath(); got != testCase.want {
				t.Errorf("LocalPath() = %q, se esperaba %q", got, testCase.want)
			}
		})
	}
}
