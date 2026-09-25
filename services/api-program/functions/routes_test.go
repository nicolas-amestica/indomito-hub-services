package functions

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"

	"github.com/labstack/echo/v4"
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

// serverlessPolicyDeclarationPattern captura una declaracion de `policies`, sea
// del endpoint o del servicio.
//
// Anclada igual que la anterior, y por el mismo motivo: los comentarios de
// serverless.ts explican precisamente por que no hay politicas y mencionan la
// palabra varias veces.
var serverlessPolicyDeclarationPattern = regexp.MustCompile(
	`(?m)^\s+policies:`,
)

// declaredRoutes son las rutas del servicio, en el mismo orden en que
// serverless.ts declara sus endpoints.
func declaredRoutes() []Route {
	return []Route{
		GenerateBudgetRoute,
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
// a una sola: el README del repo advierte que es facil desincronizarlas y que el
// sintoma es un 404 que solo aparece en AWS, nunca en el servidor local. Este
// test convierte ese desajuste en un build roto.
//
// Comprueba tambien la cuenta, que en este servicio es el Requirement 17.1:
// api-program expone un solo endpoint.
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

// TestServerlessEndpointsRequireAuthorizer comprueba que el endpoint siga
// declarado como protegido.
//
// Es el Requirement 19.3 escrito como test. El endpoint produce un documento
// comercial con los precios de una cotizacion y consume compute de 512 MB por
// llamada, asi que un `public: true` aqui no seria un ajuste de configuracion:
// dejaria a cualquiera generar presupuestos con el diseno de la empresa y gastar
// su cuota de Lambda. La consecuencia deliberada es que el servicio no se
// despliega mientras no exista el authorizer (19.4), y ese bloqueo lo impone el
// builder de common/go-service.ts al evaluar serverless.ts.
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
				"endpoint %d declara public: %s — el presupuesto genera un documento comercial e invoca compute facturable, no puede ser publico (Requirement 19.3)",
				index, match[1],
			)
		}
	}
}

// TestServerlessDeclaresNoDynamodbAccess comprueba que serverless.ts siga sin
// declarar politicas IAM.
//
// Es la version comprobable de los Requirements 17.9 y 17.12. El builder de
// common/go-service.ts solo arma un rol propio para una funcion que declare
// `policies`, y solo emite `provider.iam` si el servicio declara las suyas: sin
// ninguna de las dos, el rol del servicio no alcanza ninguna tabla. Ese es todo
// el mecanismo, y es la razon por la que este servicio no puede consultar
// DynamoDB aunque alguien escribiera el codigo para intentarlo.
//
// El test mira la declaracion y no solo el nombre `dynamodb` porque cualquier
// politica basta para romper la propiedad: la ausencia total es mas facil de
// verificar y mas dificil de erosionar que una lista de servicios prohibidos.
func TestServerlessDeclaresNoDynamodbAccess(t *testing.T) {
	source := readServerlessConfig(t)

	if matches := serverlessPolicyDeclarationPattern.FindAllString(source, -1); matches != nil {
		t.Errorf(
			"serverless.ts declara %d bloques `policies` — api-program opera sin acceso a ninguna tabla (Requirements 17.9 y 17.12)",
			len(matches),
		)
	}
}

// TestLocalPathUsesEchoNotation comprueba la traduccion del path del API Gateway
// a la de Echo.
//
// Es la unica diferencia admitida entre lo que se despliega y lo que registra el
// servidor local, y esta acotada a la notacion.
//
// El caso que importa aqui es el de los dos puntos de `:presupuesto`. Sin la
// barra invertida, Echo no registraria una ruta estatica sino el nodo `/programas`
// mas un parametro llamado `presupuesto`, que ademas haria coincidir
// `/programasCualquierCosa`. El bug no aparece en un 404: aparece en un endpoint
// que responde a paths que nunca se declararon.
func TestLocalPathUsesEchoNotation(t *testing.T) {
	cases := []struct {
		name  string
		route Route
		want  string
	}{
		{
			name:  "accion con dos puntos escapados",
			route: GenerateBudgetRoute,
			want:  `/cotizaciones\:presupuesto`,
		},
		{
			name:  "parametro entre llaves",
			route: Route{Method: "GET", Path: "/programas/{id-programa}"},
			want:  "/programas/:id-programa",
		},
		{
			name:  "sin accion ni parametros",
			route: Route{Method: "GET", Path: "/programas"},
			want:  "/programas",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := testCase.route.LocalPath(); got != testCase.want {
				t.Errorf("LocalPath() = %q, se esperaba %q", got, testCase.want)
			}
		})
	}
}

// TestRegisterMatchesOnlyTheDeclaredPath comprueba en el router de Echo lo que
// [TestLocalPathUsesEchoNotation] comprueba sobre la cadena.
//
// La traduccion de notacion solo sirve si el servidor local termina respondiendo
// al path que se declaro y a ninguno mas. Sin el escape, este test falla por el
// segundo caso y no por el primero: la ruta con parametro tambien responde al
// path exacto, y por eso el error pasaria inadvertido en una prueba manual.
func TestRegisterMatchesOnlyTheDeclaredPath(t *testing.T) {
	e := echo.New()
	GenerateBudgetRoute.Register(e, func(c echo.Context) error {
		return c.NoContent(http.StatusNoContent)
	})

	cases := []struct {
		name   string
		path   string
		status int
	}{
		{
			name:   "el path declarado responde",
			path:   "/cotizaciones:presupuesto",
			status: http.StatusNoContent,
		},
		{
			name:   "un path que solo comparte el prefijo no responde",
			path:   "/cotizaciones-cualquier-cosa",
			status: http.StatusNotFound,
		},
		{
			name:   "otra accion sobre cotizaciones no responde",
			path:   "/cotizaciones:otra-cosa",
			status: http.StatusNotFound,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, testCase.path, nil)
			recorder := httptest.NewRecorder()

			e.ServeHTTP(recorder, request)

			if recorder.Code != testCase.status {
				t.Errorf(
					"POST %s devolvio %d, se esperaba %d",
					testCase.path, recorder.Code, testCase.status,
				)
			}
		})
	}
}
