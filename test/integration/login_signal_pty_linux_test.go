//go:build integration && linux

package integration

import (
	"bytes"
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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// `pig login`, `pig logout` and `pig auth login` end on SIGINT, SIGTERM and SIGHUP wherever they wait: a built pig on a pseudo-terminal, each given a Ctrl+C
// (the byte the terminal turns into SIGINT, or that a raw prompt reads), a SIGTERM and a SIGHUP. `pig login` and `pig logout` port pi-ai's cli.ts, where Node's
// default applies: the process dies by the signal, prints nothing and writes no credential, and so does a PiG that has no handler of its own (`pig login github-copilot`).
// `pig auth login` is Pi's `pi auth login`: Ctrl+C aborts the login (`Login cancelled.`, exit 1), and the terminal is cooked again after every ending.
// Before the fix a registry installed for the provider menu, for `pig logout` and for extension providers swallowed SIGINT and SIGTERM for the whole command
// (auth_contributions.go: signal.NotifyContext with no reader), and no login or prompt ever saw the end.

// loginSignalProxy is a proxy that tunnels no connection: it answers CONNECT for every host itself, over TLS with a certificate of its own CA, as a device-code
// server that never grants (authorization_pending for ever). The pig under test reaches it through HTTPS_PROXY and SSL_CERT_FILE, so no request leaves the machine.
type loginSignalProxy struct {
	listener net.Listener
	caPEM    []byte
	caCert   *x509.Certificate
	caKey    *ecdsa.PrivateKey

	mu    sync.Mutex
	certs map[string]*tls.Certificate
	polls int
	hosts map[string]bool
}

func startLoginSignalProxy(t *testing.T) *loginSignalProxy {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "login-signal fake CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &loginSignalProxy{listener: listener, caCert: caCert, caKey: caKey, certs: map[string]*tls.Certificate{}, hosts: map[string]bool{},
		caPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
	server := &http.Server{Handler: http.HandlerFunc(p.connect), ReadHeaderTimeout: testbudget.Wait(t)}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return p
}

func (p *loginSignalProxy) certificate(host string) (*tls.Certificate, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if cert, ok := p.certs[host]; ok {
		return cert, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(int64(len(p.certs) + 2)), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, p.caCert, &key.PublicKey, p.caKey)
	if err != nil {
		return nil, err
	}
	cert := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	p.certs[host] = cert
	return cert, nil
}

// connect answers a CONNECT with a TLS server of its own for the host the client asked for.
func (p *loginSignalProxy) connect(w http.ResponseWriter, r *http.Request) {
	hijacker, ok := w.(http.Hijacker)
	if r.Method != http.MethodConnect || !ok {
		http.Error(w, "only CONNECT", http.StatusNotFound)
		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	if _, err := conn.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		_ = conn.Close()
		return
	}
	host, _, _ := net.SplitHostPort(r.Host)
	tlsConn := tls.Server(conn, &tls.Config{GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return p.certificate(host) }})
	closed := make(chan struct{})
	once := sync.Once{}
	oneConn := &loginSignalListener{conns: make(chan net.Conn, 1), closed: closed}
	oneConn.conns <- &loginSignalConn{Conn: tlsConn, onClose: func() { once.Do(func() { close(closed) }) }}
	inner := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { p.answer(host, w, req) })}
	_ = inner.Serve(oneConn)
}

// answer is a device-code server that never grants: the request for a code gets one, every poll for a token is pending.
func (p *loginSignalProxy) answer(host string, w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.hosts[host] = true
	p.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path
	switch {
	case strings.Contains(path, "token") || strings.Contains(path, "poll"):
		p.mu.Lock()
		p.polls++
		p.mu.Unlock()
		if !strings.HasSuffix(host, "github.com") {
			w.WriteHeader(http.StatusBadRequest)
		}
		_, _ = fmt.Fprint(w, `{"error":"authorization_pending"}`)
	case strings.Contains(path, "device") && (strings.Contains(path, "code") || strings.Contains(path, "authorization")):
		_, _ = fmt.Fprintf(w, `{"device_code":"dev-123","user_code":"ABCD-EFGH","verification_uri":"https://%[1]s/login/device","verification_uri_complete":"https://%[1]s/login/device?c=ABCD-EFGH","interval":1,"expires_in":600}`, host)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprint(w, `{"error":"not_found"}`)
	}
}

func (p *loginSignalProxy) pollCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.polls
}

// loginSignalListener yields one connection, then waits for it to close.
type loginSignalListener struct {
	conns  chan net.Conn
	closed chan struct{}
}

func (l *loginSignalListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
func (l *loginSignalListener) Close() error   { return nil }
func (l *loginSignalListener) Addr() net.Addr { return &net.TCPAddr{} }

type loginSignalConn struct {
	net.Conn
	onClose func()
}

func (c *loginSignalConn) Close() error { c.onClose(); return c.Conn.Close() }

// loginPtyBuffer is what a pig wrote to its terminal.
type loginPtyBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *loginPtyBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}

func (b *loginPtyBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

// loginRun is one pig, started as the session leader of a pseudo-terminal.
type loginRun struct {
	t      *testing.T
	cmd    *exec.Cmd
	master *os.File
	out    *loginPtyBuffer
	waited chan struct{}
	// readerDone closes when the reader goroutine has collected the terminal's output to its end.
	readerDone chan struct{}
	dir        string
	agentDir   string
}

// loginRunEnv is the whole environment of a pig under test: every variable that names a home or an agent directory is its own scratch directory, no credential or
// proxy of the machine reaches it, and the Go toolchain variables are the test's own, for the extension that the registry builds.
func loginRunEnv(t *testing.T, dir string, proxy *loginSignalProxy, caPath string) []string {
	t.Helper()
	env := []string{
		"PATH=" + os.Getenv("PATH"), "TERM=xterm-256color", "LANG=C.UTF-8",
		"HOME=" + filepath.Join(dir, "home"), "PIG_HOME=" + filepath.Join(dir, "pig"), "PI_HOME=" + filepath.Join(dir, "pi"),
		"PIG_CODING_AGENT_DIR=" + filepath.Join(dir, "agent"), "PI_CODING_AGENT_DIR=" + filepath.Join(dir, "agent"),
		"HTTPS_PROXY=http://" + proxy.listener.Addr().String(), "HTTP_PROXY=http://" + proxy.listener.Addr().String(), "SSL_CERT_FILE=" + caPath,
	}
	out, err := exec.Command("go", "env", "GOCACHE", "GOMODCACHE", "GOPATH", "GOFLAGS", "GOTOOLCHAIN", "GOROOT").Output()
	if err != nil {
		t.Fatalf("go env: %v", err)
	}
	for i, name := range []string{"GOCACHE", "GOMODCACHE", "GOPATH", "GOFLAGS", "GOTOOLCHAIN", "GOROOT"} {
		if value := strings.Split(string(out), "\n")[i]; value != "" {
			env = append(env, name+"="+value)
		}
	}
	for _, name := range []string{"HOME", "PIG_HOME", "PI_HOME", "PIG_CODING_AGENT_DIR", "PI_CODING_AGENT_DIR"} {
		for _, entry := range env {
			if strings.HasPrefix(entry, name+"=") {
				_ = os.MkdirAll(strings.TrimPrefix(entry, name+"="), 0o755)
			}
		}
	}
	return env
}

func startLoginRun(t *testing.T, bin, dir string, env []string, args ...string) *loginRun {
	t.Helper()
	master, slave, err := openHangupPTY()
	if err != nil {
		t.Fatalf("open pseudo-terminal: %v", err)
	}
	run := &loginRun{t: t, master: master, out: &loginPtyBuffer{}, waited: make(chan struct{}), dir: dir, agentDir: filepath.Join(dir, "agent")}
	readerDone := make(chan struct{})
	run.readerDone = readerDone
	go func() {
		defer close(readerDone)
		buf := make([]byte, 32<<10)
		for {
			n, err := master.Read(buf)
			_, _ = run.out.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()
	run.cmd = exec.Command(bin, args...)
	run.cmd.Dir = filepath.Join(dir, "cwd")
	_ = os.MkdirAll(run.cmd.Dir, 0o755)
	run.cmd.Env = env
	run.cmd.Stdin, run.cmd.Stdout, run.cmd.Stderr = slave, slave, slave
	run.cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := run.cmd.Start(); err != nil {
		_ = slave.Close()
		_ = master.Close()
		t.Fatalf("start pig: %v", err)
	}
	_ = slave.Close()
	go func() {
		_ = run.cmd.Wait()
		close(run.waited)
	}()
	t.Cleanup(func() {
		select {
		case <-run.waited:
		default:
			_ = run.cmd.Process.Kill()
			<-run.waited
		}
		_ = master.Close()
		<-readerDone
	})
	return run
}

// expect waits until the terminal output matches pattern, and returns the submatches of the match.
func (r *loginRun) expect(pattern string) []string {
	r.t.Helper()
	re := regexp.MustCompile(pattern)
	var match []string
	if !waitFor(testbudget.Wait(r.t), func() bool {
		match = re.FindStringSubmatch(r.out.String())
		return match != nil
	}) {
		r.t.Fatalf("pig never showed %q; terminal:\n%s", pattern, r.out.String())
	}
	return match
}

func (r *loginRun) send(s string) {
	r.t.Helper()
	if _, err := r.master.WriteString(s); err != nil {
		r.t.Fatalf("write to the terminal: %v", err)
	}
}

// chooseProvider answers the provider menu with the number of name.
func (r *loginRun) chooseProvider(name string) {
	r.t.Helper()
	match := r.expect(`(?m)^\s*(\d+)\. ` + regexp.QuoteMeta(name) + `\s*$[\s\S]*Enter number \(1-\d+\): `)
	r.send(match[1] + "\r")
}

// loginSignal is how the run is ended.
type loginSignal struct {
	name string
	sig  syscall.Signal
	send func(r *loginRun)
}

var loginSignals = []loginSignal{
	{"Ctrl+C", syscall.SIGINT, func(r *loginRun) { r.send("\x03") }},
	{"SIGTERM", syscall.SIGTERM, func(r *loginRun) { _ = syscall.Kill(r.cmd.Process.Pid, syscall.SIGTERM) }},
	{"SIGHUP", syscall.SIGHUP, func(r *loginRun) { _ = syscall.Kill(r.cmd.Process.Pid, syscall.SIGHUP) }},
}

// ended waits for the pig to end and returns its wait status.
func (r *loginRun) ended() syscall.WaitStatus {
	r.t.Helper()
	select {
	case <-r.waited:
	case <-time.After(testbudget.Wait(r.t)):
		r.t.Fatalf("pig still running after the signal; terminal:\n%s", r.out.String())
	}
	// The pig has ended, but the bytes it wrote last can still be in the pseudo-terminal: its output is the reader goroutine's, complete once the master reports the end.
	select {
	case <-r.readerDone:
	case <-time.After(testbudget.Wait(r.t)):
		r.t.Fatalf("the terminal output did not end after pig did; terminal:\n%s", r.out.String())
	}
	return r.cmd.ProcessState.Sys().(syscall.WaitStatus)
}

// cooked reports whether the terminal has its line discipline back: canonical input, echo and signal characters.
func (r *loginRun) cooked() bool {
	termios, err := unix.IoctlGetTermios(int(r.master.Fd()), unix.TCGETS)
	if err != nil {
		r.t.Fatalf("read the terminal modes: %v", err)
	}
	const want = unix.ICANON | unix.ECHO | unix.ISIG
	return termios.Lflag&want == want
}

// children are the processes whose parent is pid.
func loginChildren(pid int) []int {
	var children []int
	entries, _ := os.ReadDir("/proc")
	for _, entry := range entries {
		child, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}
		fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+2:]))
		if len(fields) > 1 && fields[1] == strconv.Itoa(pid) {
			children = append(children, child)
		}
	}
	return children
}

type loginSignalCase struct {
	name string
	// auth is `pig auth login`, whose Ctrl+C is the abort of Pi's controller; the others die of every signal.
	auth bool
	args []string
	// provider is the id that auth.json must not gain; seed is the auth.json that must stay as it was.
	provider string
	seed     string
	// extension is the OAuth extension fixture this case installs; its process must be gone after the signal.
	extension bool
	// reach drives the pig to the point where it waits.
	reach func(r *loginRun)
	polls bool
	// after is what the pig prints after the signal, with the terminal's echo of ^C removed; "" leaves it unchecked.
	after string
}

const loginSignalSeed = `{"anthropic":{"type":"oauth","refresh":"r","access":"a","expires":4102444800000},"github-copilot":{"type":"oauth","refresh":"r","access":"a","expires":4102444800000}}`

func TestLoginCommandsEndOnASignal(t *testing.T) {
	bin := buildBinary(t)
	proxy := startLoginSignalProxy(t)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, proxy.caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []loginSignalCase{
		{name: "device poll (GitHub Copilot, from the menu)", args: []string{"login"}, provider: "github-copilot", polls: true, reach: func(r *loginRun) {
			r.chooseProvider("GitHub Copilot")
			r.expect(`\(blank for github\.com\)`)
			r.send("\r")
			r.expect(`Enter code: ABCD-EFGH`)
		}},
		{name: "device poll (xAI, from the menu)", args: []string{"login"}, provider: "xai", polls: true, reach: func(r *loginRun) {
			r.chooseProvider("xAI")
			r.expect(`Enter code: ABCD-EFGH`)
		}},
		{name: "browser sign-in waiting for a pasted code (Anthropic, from the menu)", args: []string{"login"}, provider: "anthropic", reach: func(r *loginRun) {
			r.chooseProvider("Anthropic")
			r.expect(`Select Anthropic login method:[\s\S]*Enter number \(1-2\): `)
			r.send("1\r")
			r.expect(`Enter code or callback URL: `)
		}},
		{name: "provider menu", args: []string{"login"}, reach: func(r *loginRun) { r.expect(`Enter number \(1-\d+\): `) }},
		{name: "logout menu", args: []string{"logout"}, seed: loginSignalSeed, reach: func(r *loginRun) { r.expect(`Select a provider to logout:[\s\S]*Enter number \(1-2\): `) }},
		{name: "extension provider waiting for a code", args: []string{"login", "conformance-oauth"}, extension: true, reach: func(r *loginRun) { r.expect(`paste the code: `) }},
		{name: "enterprise question (pig auth login)", auth: true, args: []string{"auth", "login", "github-copilot"}, provider: "github-copilot", reach: func(r *loginRun) {
			r.expect(`GitHub Enterprise URL/domain \(company\.ghe\.com\): `)
		}},
		// The secret prompt gives the terminal back and ends its line, then the login says why it ended: Pi's abort listener runs before anything else sees the abort.
		{name: "API key prompt (pig auth login --api-key)", auth: true, args: []string{"auth", "login", "groq", "--api-key"}, provider: "groq", after: "\r\nLogin cancelled.\r\n", reach: func(r *loginRun) {
			r.expect(`Enter Groq API key: `)
		}},
	}
	for _, tc := range cases {
		for _, sig := range loginSignals {
			t.Run(tc.name+"/"+sig.name, func(t *testing.T) {
				dir := t.TempDir()
				env := loginRunEnv(t, dir, proxy, caPath)
				agentDir := filepath.Join(dir, "agent")
				authPath := filepath.Join(agentDir, "auth.json")
				if tc.seed != "" {
					if err := os.WriteFile(authPath, []byte(tc.seed), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if tc.extension {
					writeLoginSignalExtension(t, filepath.Join(agentDir, "extensions", "sdk-fixture"))
				}
				run := startLoginRun(t, bin, dir, env, tc.args...)
				tc.reach(run)
				if tc.polls && !waitFor(testbudget.Wait(t), func() bool { return proxy.pollCount() > 0 }) {
					t.Fatalf("the device flow never polled; terminal:\n%s", run.out.String())
				}
				var extensionProcesses []int
				if tc.extension {
					if extensionProcesses = loginChildren(run.cmd.Process.Pid); len(extensionProcesses) == 0 {
						t.Fatal("no extension process runs under the login: the test does not reach what it pins")
					}
				}
				before := run.out.String()
				sig.send(run)
				status := run.ended()
				after := strings.TrimPrefix(run.out.String(), before)

				if tc.auth && sig.sig == syscall.SIGINT {
					if !status.Exited() || status.ExitStatus() != 1 || !strings.Contains(after, "Login cancelled.") {
						t.Errorf("Ctrl+C in pig auth login: status %v, output after %q; want exit 1 and `Login cancelled.` as pi auth login", status, after)
					}
				} else {
					if !status.Signaled() || status.Signal() != sig.sig {
						t.Errorf("status %v, output after %q; want death by %v", status, after, sig.sig)
					}
					if !tc.auth && strings.TrimSpace(strings.ReplaceAll(after, "^C", "")) != "" {
						t.Errorf("pig printed %q after the signal; cli.ts prints nothing", after)
					}
				}
				if tc.after != "" && strings.ReplaceAll(after, "^C", "") != tc.after {
					t.Errorf("pig printed %q after the signal, want %q", after, tc.after)
				}
				if !run.cooked() {
					t.Errorf("the terminal is not cooked after the signal")
				}
				content, err := os.ReadFile(authPath)
				switch {
				case tc.seed != "":
					if string(content) != tc.seed {
						t.Errorf("auth.json changed to %q", content)
					}
				case err == nil && tc.provider != "" && strings.Contains(string(content), tc.provider):
					t.Errorf("auth.json holds a credential of %s: %s", tc.provider, content)
				case err == nil && !tc.auth:
					t.Errorf("a cancelled login created auth.json: %s", content)
				}
				for _, pid := range extensionProcesses {
					if !waitFor(testbudget.Wait(t), func() bool { return processExited(pid) }) {
						t.Errorf("extension process %d still runs after the command ended by %v", pid, sig.name)
					}
				}
			})
		}
	}
}

// writeLoginSignalExtension installs the conformance fixture of the subprocess host tests, which contributes the OAuth provider conformance-oauth and asks for a code.
func writeLoginSignalExtension(t *testing.T, root string) {
	t.Helper()
	repo, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join(repo, "coding", "extension", "host", "subprocess", "testdata", "sdk-fixture", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	goMod := fmt.Sprintf("module example.com/auth-contribution-fixture\n\ngo 1.26\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v0.0.0\n\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", filepath.Join(repo, "extensions", "sdk"))
	for name, content := range map[string][]byte{"main.go": source, "go.mod": []byte(goMod)} {
		if err := os.WriteFile(filepath.Join(root, name), content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
