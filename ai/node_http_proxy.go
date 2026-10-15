package ai

// Ports packages/ai/src/utils/node-http-proxy.ts

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

const UnsupportedProxyProtocolMessage = "Unsupported proxy protocol. SOCKS and PAC proxy URLs are not supported; use an HTTP or HTTPS proxy URL."

func getProxyEnv(key string, env ProviderEnv) string {
	lower, upper := strings.ToLower(key), strings.ToUpper(key)
	for _, value := range []string{env[lower], env[upper], os.Getenv(lower), os.Getenv(upper)} {
		if value != "" {
			return value
		}
	}
	return ""
}

func proxyPort(text string) (int, bool) {
	text = jsstring.TrimStart(text)
	n := 0
	if strings.HasPrefix(text, "+") || strings.HasPrefix(text, "-") {
		n++
	}
	start := n
	for n < len(text) && text[n] >= '0' && text[n] <= '9' {
		n++
	}
	if n == start {
		return 0, false
	}
	value, err := strconv.Atoi(text[:n])
	return value, err == nil
}

func parseNoProxyEntry(entry string) (host string, port int) {
	entry = jsstring.ToLower(jsstring.Trim(entry))
	if strings.HasPrefix(entry, "[") {
		if end := strings.IndexByte(entry, ']'); end >= 0 {
			rest := entry[end+1:]
			if strings.HasPrefix(rest, ":") {
				port, _ = proxyPort(rest[1:])
			}
			return entry[1:end], port
		}
	}
	if strings.Count(entry, ":") == 1 {
		host, suffix, _ := strings.Cut(entry, ":")
		if port, ok := proxyPort(suffix); ok {
			return host, port
		}
	}
	return entry, 0
}

// stripProxyBrackets removes one pair of enclosing brackets, and only a complete pair.
func stripProxyBrackets(host string) string {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		return host[1 : len(host)-1]
	}
	return host
}

func shouldProxyHostname(hostname string, port int, env ProviderEnv) bool {
	noProxy := jsstring.ToLower(getProxyEnv("no_proxy", env))
	if noProxy == "*" {
		return false
	}
	hostname = stripProxyBrackets(jsstring.ToLower(hostname))
	for _, entry := range strings.FieldsFunc(noProxy, func(r rune) bool { return r == ',' || jsstring.IsSpace(r) }) {
		host, entryPort := parseNoProxyEntry(entry)
		if entryPort != 0 && entryPort != port {
			continue
		}
		host = stripProxyBrackets(host)
		if strings.HasPrefix(host, "*.") {
			host = host[2:]
		} else if strings.HasPrefix(host, ".") || strings.HasPrefix(host, "*") {
			host = host[1:]
		}
		if host != "" && (hostname == host || strings.HasSuffix(hostname, "."+host)) {
			return false
		}
	}
	return true
}

func defaultProxyPort(protocol string) int {
	switch protocol {
	case "ftp":
		return 21
	case "gopher":
		return 70
	case "http", "ws":
		return 80
	case "https", "wss":
		return 443
	default:
		return 0
	}
}

// ResolveHTTPProxyURLForTarget resolves scoped/process proxy aliases and NO_PROXY exclusions. It rejects non-HTTP proxy protocols before a request is sent.
func ResolveHTTPProxyURLForTarget(target string, env ProviderEnv) (*url.URL, error) {
	parsed, err := nodeurl.ParseURL(target)
	if err != nil || parsed.Hostname == "" {
		return nil, nil
	}
	protocol := strings.TrimSuffix(parsed.Protocol, ":")
	port, _ := proxyPort(parsed.Port)
	if port == 0 {
		port = defaultProxyPort(protocol)
	}
	if !shouldProxyHostname(parsed.Hostname, port, env) {
		return nil, nil
	}
	proxy := getProxyEnv(protocol+"_proxy", env)
	if proxy == "" {
		proxy = getProxyEnv("all_proxy", env)
	}
	if proxy == "" {
		return nil, nil
	}
	if !strings.Contains(proxy, "://") {
		proxy = protocol + "://" + proxy
	}
	proxyURL, err := nodeurl.ParseURL(proxy)
	if err != nil {
		quoted, _ := jsstring.MarshalJSON(proxy)
		return nil, fmt.Errorf("Invalid proxy URL %s: Invalid URL", quoted)
	}
	if proxyURL.Protocol != "http:" && proxyURL.Protocol != "https:" {
		return nil, fmt.Errorf("%s Got %s", UnsupportedProxyProtocolMessage, proxyURL.Protocol)
	}
	return url.Parse(proxyURL.Href())
}
