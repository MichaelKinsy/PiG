package coding

// pi: packages/ai/src/compat.ts

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

const bareDefaultHostChild = "PIG_TEST_BARE_DEFAULT_HOST_CHILD"

// A model PiG builds for an id its registry does not list has no api and no base URL. A request of it used to go to the default OpenAI host as chat completions with the provider's key.
// The test stands a local TLS-terminating CONNECT proxy in for that host (the child process sends to api.openai.com through HTTPS_PROXY and trusts the proxy's CA through SSL_CERT_FILE)
// and counts what arrives: the refusal must leave zero connections and zero requests. BuildModel is the builder an extension's set-model and resolve-model use (session_extension_host.go,
// ModelBuilder), so the library row is the extension row.
func TestBareModelSendsNothingToTheDefaultOpenAIHost(t *testing.T) {
	if spec := os.Getenv(bareDefaultHostChild); spec != "" {
		bareDefaultHostChildRun(t, spec)
		return
	}
	for _, provider := range []string{"groq", "anthropic", "google", "openrouter", "mistral", "xai"} {
		for _, via := range []string{"BuildModel", "buildModel-explicit-key"} {
			t.Run(provider+"/"+via, func(t *testing.T) {
				proxy := newDefaultHostProxy(t)
				command := exec.Command(os.Args[0], "-test.run=^TestBareModelSendsNothingToTheDefaultOpenAIHost$", "-test.count=1")
				command.Env = append(os.Environ(),
					bareDefaultHostChild+"="+provider+"|"+via,
					"HTTPS_PROXY="+proxy.url, "https_proxy="+proxy.url, "HTTP_PROXY="+proxy.url, "http_proxy="+proxy.url,
					"NO_PROXY=", "no_proxy=", "SSL_CERT_FILE="+proxy.caFile, "SSL_CERT_DIR="+t.TempDir(),
				)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("the child failed: %v\n%s", err, output)
				}
				if connects, requests := proxy.report(); len(connects) != 0 || len(requests) != 0 {
					t.Errorf("a bare model of %s reached the default host: CONNECT %v, requests %v", provider, connects, requests)
				}
			})
		}
	}
}

// The control of the row above: a model that does name chat completions and has no base URL goes to the default host, and the proxy sees the CONNECT, so a zero count above is a
// measurement and not a proxy that cannot be reached.
func TestDefaultHostProxyControlSeesAChatCompletionsRequest(t *testing.T) {
	if os.Getenv(bareDefaultHostChild) != "" {
		return
	}
	proxy := newDefaultHostProxy(t)
	command := exec.Command(os.Args[0], "-test.run=^TestDefaultHostProxyControlChild$", "-test.count=1")
	command.Env = append(os.Environ(), bareDefaultHostChild+"=control",
		"HTTPS_PROXY="+proxy.url, "https_proxy="+proxy.url, "HTTP_PROXY="+proxy.url, "http_proxy="+proxy.url,
		"NO_PROXY=", "no_proxy=", "SSL_CERT_FILE="+proxy.caFile, "SSL_CERT_DIR="+t.TempDir())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("the control child failed: %v\n%s", err, output)
	}
	if connects, _ := proxy.report(); len(connects) == 0 {
		t.Fatal("the proxy saw no CONNECT for a chat completions model with no base URL")
	}
}

func TestDefaultHostProxyControlChild(t *testing.T) {
	if os.Getenv(bareDefaultHostChild) != "control" {
		return
	}
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "groq", Model: "m", APIKey: "sk-fake-control", ModelMetadata: &ai.Model{ID: "m", Input: []string{"text"}}})
	t.Cleanup(func() { _ = provider.Close() })
	if stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}), ai.StreamOptions{}); err == nil {
		_ = stream.Result()
	}
}

func bareDefaultHostChildRun(t *testing.T, spec string) {
	provider, via, _ := strings.Cut(spec, "|")
	services := newRuntimeTestServices(t)
	fakeKey := "sk-fake-" + provider + "-key"
	var model *ai.Model
	var err error
	if via == "BuildModel" {
		services.Registry().SetRuntimeAPIKey(provider, fakeKey)
		model, err = BuildModel(provider+"/not-listed-anywhere", services)
	} else {
		model, err = buildModel(provider+"/not-listed-anywhere", services, fakeKey)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = model.Provider.Close() })
	if stream, err := model.Provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{SystemPrompt: "be brief", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}), ai.StreamOptions{}); err == nil {
		t.Fatalf("the stream started: %+v", stream.Result())
	}
}

type defaultHostProxy struct {
	url, caFile string
	mu          sync.Mutex
	connects    []string
	requests    []string
}

func (p *defaultHostProxy) report() (connects, requests []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.connects...), append([]string(nil), p.requests...)
}

// newDefaultHostProxy serves a CONNECT proxy that answers every tunnel with a TLS server holding a certificate for the requested host from a CA written to caFile. A request that arrives
// is recorded with its Authorization scheme and the first 6 characters of the key, which is a fake.
func newDefaultHostProxy(t *testing.T) *defaultHostProxy {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "pig test ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	proxy := &defaultHostProxy{caFile: filepath.Join(t.TempDir(), "ca.pem")}
	if err := os.WriteFile(proxy.caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy.url = "http://" + listener.Addr().String()
	var wg sync.WaitGroup
	t.Cleanup(func() { _ = listener.Close(); wg.Wait() })
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				reader := bufio.NewReader(conn)
				request, err := http.ReadRequest(reader)
				if err != nil {
					return
				}
				host := request.Host
				proxy.mu.Lock()
				proxy.connects = append(proxy.connects, request.Method+" "+host)
				proxy.mu.Unlock()
				if request.Method != http.MethodConnect {
					_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n"))
					return
				}
				name, _, _ := net.SplitHostPort(host)
				leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				leafTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
				leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
				if err != nil {
					return
				}
				_, _ = conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
				secure := tls.Server(&bufferedConn{Conn: conn, reader: reader}, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}}, NextProtos: []string{"http/1.1"}})
				inner, err := http.ReadRequest(bufio.NewReader(secure))
				if err != nil {
					return
				}
				authorization := inner.Header.Get("Authorization")
				scheme, key, _ := strings.Cut(authorization, " ")
				if len(key) > 6 {
					key = key[:6]
				}
				proxy.mu.Lock()
				proxy.requests = append(proxy.requests, fmt.Sprintf("%s https://%s%s Authorization scheme %q key starts %q", inner.Method, host, inner.URL.Path, scheme, key))
				proxy.mu.Unlock()
				_, _ = secure.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}"))
			}()
		}
	}()
	return proxy
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
