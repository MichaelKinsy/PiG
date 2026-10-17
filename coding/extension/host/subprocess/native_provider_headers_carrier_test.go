package subprocess

import "testing"

// upstream: models.ts:155 Provider.headers. The carrier an extension's provider object registers with carries the headers the extension declared,
// and none when it declared none (the wire's headers field is optional).
func TestNativeProviderCarrierExposesTheDeclaredHeaders(t *testing.T) {
	declared := map[string]string{"X-Provider": "yes"}
	proxy := &nativeProviderProxy{declaration: NativeProviderDeclaration{ID: "p", Name: "P", Headers: &declared}}
	headers := proxy.carrier().Headers
	if value := headers["X-Provider"]; len(headers) != 1 || value == nil || *value != "yes" {
		t.Fatalf("carrier headers = %v, want X-Provider=yes", headers)
	}
	none := &nativeProviderProxy{declaration: NativeProviderDeclaration{ID: "p", Name: "P"}}
	if headers := none.carrier().Headers; len(headers) != 0 {
		t.Fatalf("undeclared headers = %v, want none", headers)
	}
}
