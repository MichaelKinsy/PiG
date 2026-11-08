package contract

import (
	"net/http"
	"net/http/httptest"
	"testing"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// An invalid x-p2-relay-user fails the request (Pi's JSON.parse throws), a valid one names the user.
func TestVerifyFailsOnAUserHeaderTheBrainNeverWrites(t *testing.T) {
	access := NewRelayAccess()
	var got *RelayedRequest
	var failure error
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		got, failure = access.Verify(request)
	}))
	defer server.Close()
	call := func(user string) {
		request, _ := http.NewRequest("GET", server.URL, nil)
		request.Header.Set(RelaySecretHeader, access.Secret())
		request.Header.Set(RelayOriginHeader, "https://a.example")
		request.Header.Set(RelayShellHeader, "https://shell.example")
		request.Header.Set(RelayUserHeader, user)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
	}
	call(EncodeRelayUser(p2runtime.RelayUser{ID: "u", Email: "a@b.c"}))
	if failure != nil || got == nil || got.User == nil || got.User.Email != "a@b.c" {
		t.Fatalf("valid: %v %v", got, failure)
	}
	call("!!not-json")
	if failure == nil {
		t.Fatalf("invalid user header was admitted: %v", got)
	}
}

func TestValidRequestRefusesWhatNodeHTTPThrowsOn(t *testing.T) {
	ok := requestHead{Method: "GET", Path: "/a?b=1"}
	if validRequest("http", ok, map[string]string{"x-a": "v"}) != nil {
		t.Fatal("valid request refused")
	}
	for name, bad := range map[string]func() error{
		"path":   func() error { return validRequest("http", requestHead{Method: "GET", Path: "/a b"}, nil) },
		"method": func() error { return validRequest("http", requestHead{Method: "G ET", Path: "/"}, nil) },
		"name":   func() error { return validRequest("ws", ok, map[string]string{"bad name": "v"}) },
		"value":  func() error { return validRequest("http", ok, map[string]string{"x": "a\nb"}) },
	} {
		if bad() == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
