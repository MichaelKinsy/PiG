package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Ports apps/p2/src/runtime/owner-link.ts and apps/p2/scripts/owner-link.ts: owner links (`/owner#<token>`) and the origins
// they are for, checked by the owner-link command and the TUI launcher.

// OwnerOrigin parses an origin an owner link is for. Never allow credentials or an invitation in a path or query.
func OwnerOrigin(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" && parsed.Opaque == "" && parsed.Path == "" {
		return nil, errors.New("Invalid URL; use an http(s) origin")
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("Use an http(s) origin URL without credentials, path or query")
	}
	if parsed.Scheme != "https" {
		switch parsed.Hostname() {
		case "127.0.0.1", "localhost", "::1":
		default:
			return nil, errors.New("Remote connections require HTTPS")
		}
	}
	return parsed, nil
}

var ownerFragment = regexp.MustCompile(`^[a-f0-9]{64}$`)

func originText(link *url.URL) string {
	port := link.Port()
	if (link.Scheme == "http" && port == "80") || (link.Scheme == "https" && port == "443") {
		port = ""
	}
	host := strings.ToLower(link.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port != "" {
		host += ":" + port
	}
	return link.Scheme + "://" + host
}

// OwnerLinkToken is the invitation in value, an owner link for origin (its token in the fragment).
func OwnerLinkToken(value string, origin *url.URL) (string, error) {
	link, err := url.Parse(strings.TrimSpace(value))
	if err != nil || link.Scheme == "" || link.Host == "" {
		return "", errors.New("Invalid owner link")
	}
	if originText(link) != originText(origin) || link.User != nil || link.Path != "/owner" || link.RawQuery != "" || link.ForceQuery || !ownerFragment.MatchString(link.Fragment) {
		return "", errors.New("Use an owner link for this origin with its token in the fragment")
	}
	return link.Fragment, nil
}

// OwnerLink is `p2 owner-link [base-url]`: it asks the running server on loopback (`PORT`, 8788) for an invitation and
// returns the link to open on another device. env reads the environment (`P2_PUBLIC_URL`, `PORT`).
func OwnerLink(ctx context.Context, args []string, env func(string) string) (string, error) {
	if len(args) > 1 {
		return "", errors.New("Usage: p2 owner-link [base-url]")
	}
	portText := env("PORT")
	if portText == "" {
		portText = "8788"
	}
	base := "http://127.0.0.1:" + portText
	if public := env("P2_PUBLIC_URL"); public != "" {
		base = public
	}
	if len(args) == 1 {
		base = args[0]
	}
	origin, err := OwnerOrigin(base)
	if err != nil {
		return "", err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", errors.New("Invalid PORT")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Direct loopback, no proxy/browser headers: shell access on the machine proves ownership.
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/owner-token", port), nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil || response.StatusCode < 200 || response.StatusCode > 299 {
		if response != nil {
			_ = response.Body.Close()
		}
		return "", errors.New("Cannot mint an owner link; check the running server and PORT")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<16))
	var value struct {
		Token any `json:"token"`
	}
	if err != nil || json.Unmarshal(body, &value) != nil {
		return "", errors.New("Invalid owner-token response")
	}
	token, _ := value.Token.(string)
	if !ownerFragment.MatchString(token) {
		return "", errors.New("Invalid owner-token response")
	}
	link := *origin
	link.Path, link.Fragment = "/owner", token
	return link.String(), nil
}
