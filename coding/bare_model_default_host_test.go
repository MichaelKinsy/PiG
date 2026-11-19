package coding

// pi: packages/ai/src/compat.ts

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// A model PiG used to build for an id its registry does not list had no api and no base URL, and a request of it went to the default OpenAI host as chat completions with the provider key. Pi
// has no object for such an id: the registry's getModel answers undefined (model-runtime.ts:454-455 over ai/src/models.ts:498) and set_model answers "Model not found" (rpc-mode.ts:492-495), so
// BuildModel and buildModel refuse it at the build, with that text and no model. The model object an extension hands over (buildRequestModel, Pi's setModel object) is taken to the stream with
// its api undefined and refused there (compat.ts:244-247). The test stands a local TLS-terminating CONNECT proxy in for the default host (HTTPS_PROXY; its certificate for the requested host is
// signed by the test CA this binary trusts, test_ca_test.go) and counts what arrives on every row: zero connections and zero requests, so no Authorization header, whichever side refuses.
// BuildModel is the builder an extension set-model and resolve-model use (session_extension_host.go, ModelBuilder), so the library row is the extension row.
func TestBareModelSendsNothingToTheDefaultOpenAIHost(t *testing.T) {
	for _, provider := range []string{"groq", "anthropic", "google", "openrouter", "mistral", "xai"} {
		if provider == "mistral" && pigstrip.Has(pigstrip.ListAPIs, "mistral-conversations") {
			continue // a Piglet that strips the Mistral API has no mistral provider to build
		}
		for _, via := range []string{"BuildModel", "buildModel-explicit-key", "request-model-object", "listed-model-control"} {
			t.Run(provider+"/"+via, func(t *testing.T) {
				proxy := newDefaultHostProxy(t)
				services := newRuntimeTestServices(t)
				fakeKey := "sk-fake-" + provider + "-key"
				const id = "not-listed-anywhere"
				var model *ai.Model
				var err error
				switch via {
				case "BuildModel":
					services.Registry().SetRuntimeAPIKey(provider, fakeKey)
					model, err = BuildModel(provider+"/"+id, services)
				case "buildModel-explicit-key":
					model, err = buildModel(provider+"/"+id, services, fakeKey)
				case "request-model-object":
					services.Registry().SetRuntimeAPIKey(provider, fakeKey)
					model, err = buildRequestModel(&ai.Model{ID: id, ProviderMeta: ai.ProviderMetadata{ProviderID: provider}}, services, "")
				default:
					// The control: a model of the same provider the catalog lists reaches the proxy, so the zero of the rows above is a measurement and not a proxy nobody can reach.
					listedModels := services.ModelRuntime().GetModels(provider)
					if len(listedModels) == 0 {
						t.Fatalf("the catalog lists no model of %s", provider)
					}
					services.Registry().SetRuntimeAPIKey(provider, fakeKey)
					if model, err = BuildModel(provider+"/"+listedModels[0].ID, services); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = model.Provider.Close() })
					ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
					defer cancel()
					if stream, err := model.Provider.Stream(ctx, ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}), ai.StreamOptions{MaxRetries: new(0)}); err == nil {
						_ = stream.Result()
					}
					if connects, _ := proxy.report(); len(connects) == 0 {
						t.Errorf("control: a listed model of %s (%s) reached no CONNECT at the proxy, so the rows that count zero measure nothing", provider, listedModels[0].ID)
					}
					return
				}
				if via == "request-model-object" {
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = model.Provider.Close() })
					stream, err := model.Provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{SystemPrompt: "be brief", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}), ai.StreamOptions{})
					if err == nil {
						t.Fatalf("the stream started: error message %q", stream.Result().ErrorMessage)
					}
					if want := "No API provider registered for api: undefined"; err.Error() != want {
						t.Errorf("error = %q, want %q", err, want)
					}
				} else if want := "Model not found: " + provider + "/" + id; model != nil || err == nil || err.Error() != want {
					t.Errorf("model %+v, error %v; want no model and %q", model, err, want)
				}
				if connects, requests := proxy.report(); len(connects) != 0 || len(requests) != 0 {
					t.Errorf("a bare model of %s reached the default host: CONNECT %v, requests %v", provider, connects, requests)
				}
			})
		}
	}
}

// The control of the row above: a model that does name chat completions and has no base URL goes to the default host, and the proxy sees the CONNECT and, where the platform loads its
// roots from the environment, the request with its Authorization header, so a zero count above is a measurement and not a proxy that cannot be reached.
func TestDefaultHostProxyControlSeesAChatCompletionsRequest(t *testing.T) {
	proxy := newDefaultHostProxy(t)
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "groq", Model: "m", APIKey: "sk-fake-control", ModelMetadata: &ai.Model{ID: "m", Input: []string{"text"}}})
	t.Cleanup(func() { _ = provider.Close() })
	if stream, err := provider.Stream(t.Context(), ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hello")}}}), ai.StreamOptions{}); err == nil {
		_ = stream.Result()
	}
	connects, requests := proxy.report()
	if len(connects) == 0 {
		t.Fatal("the proxy saw no CONNECT for a chat completions model with no base URL")
	}
	if systemRootsFromEnvironment && (len(requests) != 1 || !strings.Contains(requests[0], `key starts "sk-fak"`)) {
		t.Errorf("requests at the proxy = %v, want one chat completions request with the fake key", requests)
	}
}

type defaultHostProxy struct {
	mu       sync.Mutex
	connects []string
	requests []string
}

func (p *defaultHostProxy) report() (connects, requests []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.connects...), append([]string(nil), p.requests...)
}

func (p *defaultHostProxy) addConnect(entry string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connects = append(p.connects, entry)
}

func (p *defaultHostProxy) addRequest(entry string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, entry)
}

// newDefaultHostProxy serves a CONNECT proxy and points HTTPS_PROXY and HTTP_PROXY at it for the test. It answers every tunnel with a TLS server whose certificate for the requested
// host the test CA signed. A request that arrives is recorded with its Authorization scheme and the first 6 characters of the key, which is a fake.
func newDefaultHostProxy(t *testing.T) *defaultHostProxy {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxy := &defaultHostProxy{}
	t.Setenv("HTTPS_PROXY", "http://"+listener.Addr().String())
	t.Setenv("https_proxy", "http://"+listener.Addr().String())
	t.Setenv("HTTP_PROXY", "http://"+listener.Addr().String())
	t.Setenv("http_proxy", "http://"+listener.Addr().String())
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	var wg sync.WaitGroup
	t.Cleanup(func() { _ = listener.Close(); wg.Wait() })
	wg.Go(func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			wg.Go(func() {
				defer func() { _ = conn.Close() }()
				proxy.serve(conn)
			})
		}
	})
	return proxy
}

func (p *defaultHostProxy) serve(conn net.Conn) {
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(conn)
	request, err := http.ReadRequest(reader)
	if err != nil {
		return
	}
	p.addConnect(request.Method + " " + request.Host)
	if request.Method != http.MethodConnect {
		_, _ = conn.Write([]byte("HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\n\r\n"))
		return
	}
	name, _, _ := net.SplitHostPort(request.Host)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, testCA.cert, &leafKey.PublicKey, testCA.key)
	if err != nil {
		return
	}
	_, _ = conn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n"))
	secure := tls.Server(&bufferedConn{Conn: conn, reader: reader}, &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}}, NextProtos: []string{"http/1.1"}})
	if err := secure.Handshake(); err != nil {
		return
	}
	inner, err := http.ReadRequest(bufio.NewReader(secure))
	if err != nil {
		return
	}
	scheme, key, _ := strings.Cut(inner.Header.Get("Authorization"), " ")
	p.addRequest(fmt.Sprintf("%s https://%s%s Authorization scheme %q key starts %q (a fake key)", inner.Method, request.Host, inner.URL.Path, scheme, key[:min(len(key), 6)]))
	_, _ = secure.Write([]byte("HTTP/1.1 400 Bad Request\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}"))
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(buffer []byte) (int, error) { return c.reader.Read(buffer) }
