package functions

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

var serverlessEndpointPattern = regexp.MustCompile(`name:\s*["'][^"']+["'],\s*\n\s*method:\s*["']([A-Z]+)["'],\s*\n\s*path:\s*["']([^"']+)["']`)

func TestImplementedRoutesMatchServerless(t *testing.T) {
	source, err := os.ReadFile("../serverless.ts")
	if err != nil {
		t.Fatal(err)
	}
	matches := serverlessEndpointPattern.FindAllStringSubmatch(string(source), -1)
	if len(matches) != len(ImplementedRoutes) {
		t.Fatalf("serverless endpoints = %d, implemented routes = %d", len(matches), len(ImplementedRoutes))
	}
	for index, route := range ImplementedRoutes {
		if matches[index][1] != route.Method || matches[index][2] != externalRoutePath(route.Path) {
			t.Fatalf("serverless route %s %s != Go route %s %s", matches[index][1], matches[index][2], route.Method, route.Path)
		}
	}
}

func externalRoutePath(path string) string {
	segments := strings.Split(path, "/")
	for index, segment := range segments {
		if strings.HasPrefix(segment, ":") {
			name, suffix, _ := strings.Cut(segment[1:], ":")
			segments[index] = "{" + name + "}"
			if suffix != "" {
				segments[index] += ":" + suffix
			}
		}
	}
	return strings.Join(segments, "/")
}
