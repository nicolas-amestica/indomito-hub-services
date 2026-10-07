package paymentaccess

import (
	"strings"
	"testing"
)

func TestRUTLookupCanonicalAndStrict(t *testing.T) {
	secret := strings.Repeat("test", 8)
	want, err := RUTKey(secret, "12.345.678-5")
	if err != nil {
		t.Fatal(err)
	}
	for _, rut := range []string{"123456785", "12345678-5", " 12.345.678-5 "} {
		got, err := RUTKey(secret, rut)
		if err != nil || got != want {
			t.Fatalf("noncanonical RUT %s", rut)
		}
	}
	for _, rut := range []string{"12345678-9", "12345678", "1-9", "not-a-rut", "000000000000"} {
		if _, err := RUTKey(secret, rut); err == nil {
			t.Fatalf("invalid RUT accepted %s", rut)
		}
	}
	if strings.Contains(want, "12345678") {
		t.Fatal("clear RUT in lookup")
	}
}
func TestLookupDomainsAndCodes(t *testing.T) {
	secret := strings.Repeat("test", 8)
	a, err := CodeKey(secret, " ab23cd ")
	if err != nil {
		t.Fatal(err)
	}
	b, err := CodeKey(secret, "AB23CD")
	if err != nil || a != b {
		t.Fatal("code normalization")
	}
	for _, code := range []string{"AB01CD", "ABC", "ABCDEFG", "AB/CD2"} {
		if _, err := CodeKey(secret, code); err == nil {
			t.Fatal("invalid code accepted")
		}
	}
	if _, err := CodeKey("short", "AB23CD"); err == nil {
		t.Fatal("short secret accepted")
	}
	x, err := Digest(secret, "one", "value")
	if err != nil {
		t.Fatal(err)
	}
	y, err := Digest(secret, "two", "value")
	if err != nil || x == y {
		t.Fatal("purposes not separated")
	}
}
