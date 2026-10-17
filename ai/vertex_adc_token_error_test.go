//go:build !pig_strip_google_vertex

package ai

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Pi's Vertex client gets its ADC token through google-auth-library, whose gaxios rejects a failed token request with
// node-fetch's FetchError text: "request to <token url> failed, reason: connect ECONNREFUSED <address>:<port>" (probed: Pi
// 1.1.0 with an unreachable proxy). retry.ts does not retry it; PiG's "Vertex ADC token: Post …: connection refused" matched
// "connection.?refused" and was retried.
func TestVertexADCTokenConnectFailureIsGaxiosTextLikePi(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	address := closedTCPAddress(t, "tcp4", "127.0.0.1:0")
	tokenURL := "http://" + address.String() + "/token"
	file, err := json.Marshal(map[string]string{
		"type":         "service_account",
		"client_email": "pig@example.iam.gserviceaccount.com",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: must(x509.MarshalPKCS8PrivateKey(key))})),
		"token_uri":    tokenURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "adc.json")
	if err := os.WriteFile(path, file, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = vertexAccessToken(t.Context(), ProviderEnv{"GOOGLE_APPLICATION_CREDENTIALS": path})
	want := "request to " + tokenURL + " failed, reason: connect ECONNREFUSED " + address.String()
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	// retry.ts also matches status codes such as "520" anywhere in the text, so a random port can make any message retryable; the
	// probe's port 9 stands in for it.
	text := strings.ReplaceAll(err.Error(), strconv.Itoa(address.Port), "9")
	// Pi 0fbdbd3f (#10996) retries this node-fetch wording ("request to \\S+ failed, reason:").
	if !IsRetryableAssistantError(AssistantMessage{StopReason: StopReasonError, ErrorMessage: text}) {
		t.Fatalf("%q is not retryable; Pi's retry.ts matches it", text)
	}
}

func must[T any](value T, err error) T {
	if err != nil {
		panic(err)
	}
	return value
}
