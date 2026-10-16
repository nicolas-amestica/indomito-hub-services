package functions

import (
	"strings"
	"testing"
)

func TestRoutesAreUniqueAndCanonical(t *testing.T) {
	seen := make(map[string]struct{}, len(Routes))
	for _, route := range Routes {
		key := route.Method + " " + route.Path
		if _, exists := seen[key]; exists {
			t.Errorf("ruta duplicada: %s", key)
		}
		seen[key] = struct{}{}
		if !strings.HasPrefix(route.Path, "/") {
			t.Errorf("ruta sin slash inicial: %s", route.Path)
		}
		if strings.ContainsAny(route.Path, "ÁÉÍÓÚáéíóú ") {
			t.Errorf("ruta no canónica: %s", route.Path)
		}
	}
}

func TestRoutesContainRequiredPublicWebhooks(t *testing.T) {
	wanted := map[string]bool{
		"GET /webhooks/whatsapp":  false,
		"POST /webhooks/whatsapp": false,
	}
	for _, route := range Routes {
		key := route.Method + " " + route.Path
		if _, exists := wanted[key]; exists {
			wanted[key] = true
		}
	}
	for route, found := range wanted {
		if !found {
			t.Errorf("falta la ruta %s", route)
		}
	}
}
