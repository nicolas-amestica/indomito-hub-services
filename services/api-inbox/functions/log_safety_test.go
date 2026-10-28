package functions

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestInboxLogsDoNotExposeDirectCustomerIdentifiersOrSecrets(t *testing.T) {
	logCall := regexp.MustCompile(`log\.(?:Printf|Println|Print)\([^\n]*`)
	for _, root := range []string{".", "../domain"} {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(root, entry.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, call := range logCall.FindAllString(string(raw), -1) {
				lower := strings.ToLower(call)
				for _, forbidden := range []string{"messageid", "phone", "email", "accesstoken", "appsecret", "verifytoken"} {
					if strings.Contains(lower, forbidden) {
						t.Fatalf("unsafe log field %q in %s: %s", forbidden, path, call)
					}
				}
			}
		}
	}
}
