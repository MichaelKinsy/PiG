package runtime

import (
	"encoding/json"
	"net/http"
	"strings"
)

// Ports apps/p2/src/runtime/http.ts: HTTP helpers shared by the server's routes.

// SendJSON writes a JSON response: the status, `Content-Type`, then headers (a `Set-Cookie`, say), and the body as
// JSON.
func SendJSON(response http.ResponseWriter, status int, body any, headers map[string]string) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	response.Header().Set("Content-Type", "application/json")
	for name, value := range headers {
		response.Header().Set(name, value)
	}
	response.WriteHeader(status)
	_, err = response.Write(encoded)
	return err
}

// CookieValue is the value of the request's cookie name (the first one), if it carries one.
func CookieValue(request *http.Request, name string) (string, bool) {
	prefix := name + "="
	for _, part := range strings.Split(strings.Join(request.Header.Values("Cookie"), "; "), ";") {
		if trimmed := strings.TrimSpace(part); strings.HasPrefix(trimmed, prefix) {
			return trimmed[len(prefix):], true
		}
	}
	return "", false
}

// FromOrigin reports a state-changing request from a page of this origin: exactly its `Origin` header, which a
// cross-site form or fetch cannot send. (A missing `Origin` does not count.)
func FromOrigin(request *http.Request, origin string) bool {
	values := request.Header.Values("Origin")
	return len(values) == 1 && values[0] == origin
}
