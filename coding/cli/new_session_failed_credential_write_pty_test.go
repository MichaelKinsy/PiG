//go:build linux

package cli

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// A refreshed OAuth credential is written back to auth.json, and the write can fail half way (a full file system, a quota). Pi writes the file in place, so a
// write cut short leaves a truncated file: every provider reads as signed out, the next prompt fails to read the store, and /new greets the user with the
// login guidance although they were signed in. PiG writes through a temporary file and a rename, so the old file stays whole and /new keeps working.
func TestInteractiveNewSessionAfterFailedCredentialWriteKeepsTheLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the pig binary")
	}
	t.Parallel()
	// A CA, and a certificate for api.github.com and the loopback address: the refresh goes to api.github.com through a proxy that lands on the test server.
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leafTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "api.github.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, DNSNames: []string{"api.github.com"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	recorder := &modelCallRecorder{}
	var port int
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/copilot_internal/v2/token":
			_, _ = fmt.Fprintf(w, `{"token":"tid=fresh;exp=4102444800;proxy-ep=127.0.0.1:%d","expires_at":4102444800}`, port)
		case r.URL.Path == "/models":
			_, _ = fmt.Fprint(w, `{"data":[{"id":"gemini-3.8-flash","model_picker_enabled":true,"policy":{"state":"enabled"},"capabilities":{"supports":{"tool_calls":true}}}]}`)
		case strings.HasSuffix(r.URL.Path, "/chat/completions"):
			recorder.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}}}
	server.StartTLS()
	t.Cleanup(server.Close)
	port = server.Listener.Addr().(*net.TCPAddr).Port
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		upstream, err := net.Dial("tcp", server.Listener.Addr().String())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			return
		}
		_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close() }()
		go func() { _, _ = io.Copy(client, upstream); _ = client.Close() }()
	}))
	t.Cleanup(proxy.Close)

	home := shortTempDir(t)
	agentDir := filepath.Join(home, "agent")
	seedFirstRunDone(t, agentDir)
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"defaultProvider":"github-copilot","defaultModel":"gemini-3.8-flash"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// The second provider's entry makes auth.json larger than the file size limit the process runs under, so the rewrite after a refresh is cut short.
	writeAuth := func(expires int64) []byte {
		t.Helper()
		data, err := json.Marshal(map[string]any{
			"github-copilot": map[string]any{"type": "oauth", "refresh": "ghu_FAKEREFRESH", "access": fmt.Sprintf("tid=1;exp=4102444800;proxy-ep=127.0.0.1:%d", port), "expires": expires, "availableModelIds": []string{"gemini-3.8-flash"}},
			"zz-filler":      map[string]any{"type": "api_key", "key": strings.Repeat("k", 1500)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(agentDir, "auth.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		return data
	}
	writeAuth(time.Now().Add(time.Hour).UnixMilli())
	caPath := filepath.Join(home, "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}

	master, slave := openPTY(t, 40, 140)
	t.Cleanup(func() { _ = master.Close() })
	// The shell lowers the file size limit to 1 KiB for pig alone (SIGXFSZ ignored, so an oversized write fails with EFBIG instead of killing it).
	cmd := exec.Command("/bin/sh", "-c", `trap '' XFSZ; ulimit -f 2; exec "$@"`, "sh", buildPigBinaryForSignalTest(t), "--no-extensions", "--no-session")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "HOME="+home, "PIG_HOME="+home, "PI_HOME="+home, "PIG_CODING_AGENT_DIR="+agentDir, "PI_CODING_AGENT_DIR="+agentDir,
		"PIG_SKIP_VERSION_CHECK=1", "PI_SKIP_VERSION_CHECK=1", "SSL_CERT_FILE="+caPath, "HTTPS_PROXY="+proxy.URL, "HTTP_PROXY="+proxy.URL, "ALL_PROXY="+proxy.URL, "https_proxy="+proxy.URL, "http_proxy="+proxy.URL, "TERM=xterm-256color")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pig: %v", err)
	}
	_ = slave.Close()
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	out := &ptyOutput{}
	go func() { _, _ = io.Copy(out, master) }()
	submit := func(text string) {
		t.Helper()
		if _, err := master.WriteString(text); err != nil {
			t.Fatal(err)
		}
		time.Sleep(300 * time.Millisecond)
		if _, err := master.WriteString("\r"); err != nil {
			t.Fatal(err)
		}
	}

	waitForScreen(t, out, 0, "(auto)", "start-up")
	time.Sleep(time.Second)
	submit("first question")
	waitForScreen(t, out, 0, "ANSWER-NUMBER-1", "the first prompt, signed in")

	// The access token expires: the next request refreshes it, and the write of the refreshed credential fails.
	writeAuth(1)
	whole, err := os.ReadFile(filepath.Join(agentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	mark := out.mark()
	submit("second question")
	waitForScreen(t, out, mark, "Credential store modify failed", "the prompt whose credential write fails")
	time.Sleep(time.Second)

	mark = out.mark()
	submit("/new")
	waitForScreen(t, out, mark, "New session started", "/new")
	time.Sleep(time.Second)
	mark = out.mark()
	submit("third question")
	deadline := time.Now().Add(testbudget.Wait(t))
	for {
		screen := string(out.since(mark))
		if strings.Contains(screen, "No API key found") || strings.Contains(screen, "Error:") || strings.Contains(screen, "ANSWER-NUMBER-2") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the prompt after /new produced neither an answer nor an error; tail: %q", tail(out.since(mark), 1500))
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(time.Second)
	if screen := string(out.since(mark)); strings.Contains(screen, "No API key found") || strings.Contains(screen, "Use /login") {
		t.Errorf("/new after a failed credential write asks the signed-in user to log in; tail: %q", tail(out.since(mark), 1200))
	}
	if after, err := os.ReadFile(filepath.Join(agentDir, "auth.json")); err != nil || !bytes.Equal(after, whole) {
		t.Errorf("auth.json after the failed write = %d bytes (err %v), want the %d bytes it held before", len(after), err, len(whole))
	}
}
