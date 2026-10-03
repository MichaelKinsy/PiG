// Package termuxenv prepares a PiG process for Termux on Android.
//
// Termux has no /etc and no /tmp. PiG's Android build links the C library
// (cgo), so Go resolves names through bionic and trusts Android's system
// certificates. This package sets the defaults Termux's own tools use, and
// gives a build without cgo the resolver configuration Go cannot find: without
// cgo Go's resolver falls back to the name servers 127.0.0.1 and ::1 and every
// lookup fails (golang/go#8877).
package termuxenv

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

// IsTermux reports whether the process runs in Termux, which sets
// TERMUX_VERSION. Pi's clipboard and TUI code detect Termux the same way.
func IsTermux(getenv func(string) string) bool {
	return getenv("TERMUX_VERSION") != ""
}

// hostResolvConf is where Go's resolver reads its configuration.
const hostResolvConf = "/etc/resolv.conf"

// defaultNameServers are the servers Go's resolver tries when it has no
// configuration (net.defaultNS).
var defaultNameServers = []string{"127.0.0.1:53", "[::1]:53"}

// Configure adapts the process to Termux and does nothing elsewhere. Call it
// before the first network request or temporary file. It
//   - sets TMPDIR to $PREFIX/tmp when it is unset, as Termux's profile does;
//   - sets SSL_CERT_FILE to Termux's CA bundle when it is unset, so TLS trusts
//     the same certificates as Termux's own tools;
//   - without cgo, replaces net.DefaultResolver with one that reads
//     $PREFIX/etc/resolv.conf when /etc/resolv.conf is missing.
func Configure() {
	if !IsTermux(os.Getenv) {
		return
	}
	prefix := os.Getenv("PREFIX")
	if dir := tempDirFor(os.Getenv("TMPDIR"), prefix); dir != "" {
		_ = os.Setenv("TMPDIR", dir)
	}
	if file := certFileFor(os.Getenv("SSL_CERT_FILE"), prefix); file != "" {
		_ = os.Setenv("SSL_CERT_FILE", file)
	}
	if cgoResolver {
		return
	}
	_, err := os.Stat(hostResolvConf)
	if resolver := resolverFor(err == nil, prefix); resolver != nil {
		net.DefaultResolver = resolver
	}
}

// tempDirFor returns the Termux temporary directory to export as TMPDIR, or ""
// to leave TMPDIR alone: when it is set, or $PREFIX/tmp is not a directory.
// Without TMPDIR Go uses /tmp, which Termux does not have.
func tempDirFor(current, prefix string) string {
	if current != "" || prefix == "" {
		return ""
	}
	dir := filepath.Join(prefix, "tmp")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ""
	}
	return dir
}

// certFileFor returns the Termux CA bundle Go should trust, or "" to leave
// SSL_CERT_FILE alone: when the user set it, or $PREFIX has no bundle.
func certFileFor(current, prefix string) string {
	if current != "" || prefix == "" {
		return ""
	}
	bundle := filepath.Join(prefix, "etc", "tls", "cert.pem")
	if info, err := os.Stat(bundle); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	return bundle
}

// resolverFor returns the resolver Configure installs, or nil to keep Go's:
// none when the host has a resolv.conf Go reads, or when $PREFIX names no
// server.
func resolverFor(hostHasResolvConf bool, prefix string) *net.Resolver {
	if hostHasResolvConf || prefix == "" {
		return nil
	}
	return resolverFromFile(filepath.Join(prefix, "etc", "resolv.conf"))
}

// resolverFromFile returns a resolver for the name servers in the resolv.conf
// at path, or nil when the file is unreadable or names none.
func resolverFromFile(path string) *net.Resolver {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	servers := parseNameServers(data)
	if len(servers) == 0 {
		return nil
	}
	return newResolver(servers)
}

// parseNameServers returns the "host:53" address of each nameserver line that
// holds an IP address, in file order.
func parseNameServers(resolvConf []byte) []string {
	var servers []string
	scanner := bufio.NewScanner(bytes.NewReader(resolvConf))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		address, err := netip.ParseAddr(fields[1])
		if err != nil {
			continue
		}
		servers = append(servers, net.JoinHostPort(address.String(), "53"))
	}
	return servers
}

// newResolver returns a resolver that sends the queries Go addresses to its
// default name servers to servers instead, the first default to the first
// server and the second to the second (or the first when there is one). An
// address that is not a default passes through.
func newResolver(servers []string) *net.Resolver {
	redirect := make(map[string]string, len(defaultNameServers))
	for i, defaultServer := range defaultNameServers {
		redirect[defaultServer] = servers[i%len(servers)]
	}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			if server, ok := redirect[address]; ok {
				address = server
			}
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, address)
		},
	}
}
