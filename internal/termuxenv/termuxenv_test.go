package termuxenv

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestParseNameServers(t *testing.T) {
	for name, tc := range map[string]struct {
		input string
		want  []string
	}{
		"termux default":  {"nameserver 8.8.8.8\nnameserver 8.8.4.4\n", []string{"8.8.8.8:53", "8.8.4.4:53"}},
		"ipv6":            {"nameserver 2001:4860:4860::8888\n", []string{"[2001:4860:4860::8888]:53"}},
		"comments":        {"# nameserver 1.1.1.1\n; nameserver 9.9.9.9\nnameserver 8.8.8.8\n", []string{"8.8.8.8:53"}},
		"tabs and extras": {"search lan\nnameserver\t1.1.1.1 # cloudflare\noptions ndots:1\n", []string{"1.1.1.1:53"}},
		"hostname":        {"nameserver dns.example\n", nil},
		"no address":      {"nameserver\n", nil},
		"empty":           {"", nil},
	} {
		if got := parseNameServers([]byte(tc.input)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: parseNameServers = %q, want %q", name, got, tc.want)
		}
	}
}

// listen returns a UDP socket standing in for a name server.
func listen(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn.LocalAddr().String()
}

func TestResolverRedirectsGoDefaultServersToTheConfiguredOnes(t *testing.T) {
	first, second := listen(t), listen(t)
	resolver := newResolver([]string{first, second})
	if !resolver.PreferGo {
		t.Fatal("resolver does not use Go's resolver")
	}
	for defaultServer, want := range map[string]string{"127.0.0.1:53": first, "[::1]:53": second} {
		conn, err := resolver.Dial(context.Background(), "udp", defaultServer)
		if err != nil {
			t.Fatalf("dial %s: %v", defaultServer, err)
		}
		if got := conn.RemoteAddr().String(); got != want {
			t.Errorf("default server %s reached %s, want %s", defaultServer, got, want)
		}
		_ = conn.Close()
	}
}

func TestResolverWithOneServerUsesItForBothDefaults(t *testing.T) {
	only := listen(t)
	resolver := newResolver([]string{only})
	for _, defaultServer := range defaultNameServers {
		conn, err := resolver.Dial(context.Background(), "udp", defaultServer)
		if err != nil {
			t.Fatal(err)
		}
		if got := conn.RemoteAddr().String(); got != only {
			t.Errorf("default server %s reached %s, want %s", defaultServer, got, only)
		}
		_ = conn.Close()
	}
}

func TestResolverLeavesOtherServersAlone(t *testing.T) {
	configured, other := listen(t), listen(t)
	conn, err := newResolver([]string{configured}).Dial(context.Background(), "udp", other)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if got := conn.RemoteAddr().String(); got != other {
		t.Fatalf("a configured resolv.conf server reached %s, want %s", got, other)
	}
}

func TestResolverFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolv.conf")
	if resolverFromFile(path) != nil {
		t.Fatal("a missing resolv.conf produced a resolver")
	}
	if err := os.WriteFile(path, []byte("# no servers\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if resolverFromFile(path) != nil {
		t.Fatal("a resolv.conf without servers produced a resolver")
	}
	if err := os.WriteFile(path, []byte("nameserver 8.8.8.8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if resolverFromFile(path) == nil {
		t.Fatal("a resolv.conf with a server produced no resolver")
	}
}

func writeTermuxFile(t *testing.T, prefix, name, content string) {
	t.Helper()
	path := filepath.Join(prefix, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolverForNeedsNoHostResolvConfAndATermuxFile(t *testing.T) {
	prefix := t.TempDir()
	writeTermuxFile(t, prefix, "etc/resolv.conf", "nameserver 8.8.8.8\n")
	for name, tc := range map[string]struct {
		hostResolv    bool
		prefix        string
		wantInstalled bool
	}{
		"termux":               {false, prefix, true},
		"host has resolv.conf": {true, prefix, false},
		"no PREFIX":            {false, "", false},
		"no file":              {false, t.TempDir(), false},
	} {
		if got := resolverFor(tc.hostResolv, tc.prefix) != nil; got != tc.wantInstalled {
			t.Errorf("%s: resolver installed = %v, want %v", name, got, tc.wantInstalled)
		}
	}
}

func TestCertFileForTrustsTheTermuxBundleUnlessTheUserChoseOne(t *testing.T) {
	prefix := t.TempDir()
	if got := certFileFor("", prefix); got != "" {
		t.Fatalf("no bundle: certFileFor = %q", got)
	}
	writeTermuxFile(t, prefix, "etc/tls/cert.pem", "pem\n")
	bundle := filepath.Join(prefix, "etc", "tls", "cert.pem")
	if got := certFileFor("", prefix); got != bundle {
		t.Fatalf("certFileFor = %q, want %q", got, bundle)
	}
	if got := certFileFor("/custom/ca.pem", prefix); got != "" {
		t.Fatalf("user SSL_CERT_FILE overridden: %q", got)
	}
	if got := certFileFor("", ""); got != "" {
		t.Fatalf("no PREFIX: certFileFor = %q", got)
	}
	if got := certFileFor("", filepath.Join(prefix, "etc")); got != "" {
		t.Fatalf("a directory is not a bundle: %q", got)
	}
}

func TestIsTermuxFollowsTERMUXVERSION(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if !IsTermux(env(map[string]string{"TERMUX_VERSION": "0.118.3"})) {
		t.Error("TERMUX_VERSION set: not Termux")
	}
	if IsTermux(env(map[string]string{"PREFIX": "/data/data/com.termux/files/usr"})) {
		t.Error("PREFIX alone is not Termux")
	}
}

func TestTempDirForDefaultsToTheTermuxTmpOnlyWhenUnset(t *testing.T) {
	prefix := t.TempDir()
	if got := tempDirFor("", prefix); got != "" {
		t.Fatalf("no $PREFIX/tmp: tempDirFor = %q", got)
	}
	tmp := filepath.Join(prefix, "tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := tempDirFor("", prefix); got != tmp {
		t.Fatalf("tempDirFor = %q, want %q", got, tmp)
	}
	if got := tempDirFor("/custom/tmp", prefix); got != "" {
		t.Fatalf("user TMPDIR overridden: %q", got)
	}
	if got := tempDirFor("", ""); got != "" {
		t.Fatalf("no PREFIX: tempDirFor = %q", got)
	}
}

func TestConfigureChangesNothingOutsideTermux(t *testing.T) {
	prefix := t.TempDir()
	writeTermuxFile(t, prefix, "etc/resolv.conf", "nameserver 8.8.8.8\n")
	writeTermuxFile(t, prefix, "etc/tls/cert.pem", "pem\n")
	writeTermuxFile(t, prefix, "tmp/.keep", "")
	t.Setenv("TERMUX_VERSION", "")
	t.Setenv("PREFIX", prefix)
	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("TMPDIR", "")
	before := net.DefaultResolver
	Configure()
	if net.DefaultResolver != before || os.Getenv("SSL_CERT_FILE") != "" || os.Getenv("TMPDIR") != "" {
		t.Fatal("Configure changed the process outside Termux")
	}
}

func TestConfigureSetsTheTermuxDefaults(t *testing.T) {
	prefix := t.TempDir()
	writeTermuxFile(t, prefix, "etc/tls/cert.pem", "pem\n")
	writeTermuxFile(t, prefix, "tmp/.keep", "")
	t.Setenv("TERMUX_VERSION", "0.118.3")
	t.Setenv("PREFIX", prefix)
	t.Setenv("SSL_CERT_FILE", "")
	t.Setenv("TMPDIR", "")
	before := net.DefaultResolver
	t.Cleanup(func() { net.DefaultResolver = before })
	Configure()
	if want := filepath.Join(prefix, "etc", "tls", "cert.pem"); os.Getenv("SSL_CERT_FILE") != want {
		t.Fatalf("SSL_CERT_FILE = %q, want %q", os.Getenv("SSL_CERT_FILE"), want)
	}
	if want := filepath.Join(prefix, "tmp"); os.Getenv("TMPDIR") != want {
		t.Fatalf("TMPDIR = %q, want %q", os.Getenv("TMPDIR"), want)
	}
}
