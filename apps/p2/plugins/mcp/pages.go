package mcp

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/nodeurl"
)

// Ports apps/p2/plugins/mcp/pages.ts: the browser pages of MCP sign-ins: a result (signed in, or why not) and the page that
// finds out whether the browser runs on the p2 host before choosing where the sign-in returns. Plain HTML with their own
// strict CSP.

// ResultPage is a page saying title, then message.
func ResultPage(title, message string) string {
	return document(title, "<h1>"+escapeHTML(title)+"</h1><p>"+escapeHTML(message)+"</p>")
}

// SendPage sends html with status and a CSP allowing nothing but inline styles (and extra directives).
func SendPage(response http.ResponseWriter, status int, html string, extra ...string) {
	policy := append([]string{
		"default-src 'none'",
		"style-src 'unsafe-inline'",
		"base-uri 'none'",
		"form-action 'none'",
		"frame-ancestors 'none'",
	}, extra...)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Content-Security-Policy", strings.Join(policy, "; "))
	response.WriteHeader(status)
	_, _ = response.Write([]byte(html))
}

// SendProbePage sends the page that asks the p2 host's loopback probe at probeURL whether this browser runs there, then
// continues the sign-in to server with `?via=loopback` (it does) or `?via=callback` (it does not, or the browser blocks the
// request). While the answer takes long (a browser asking the user for local network access), the page offers both.
func SendProbePage(response http.ResponseWriter, server, probeURL string) error {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return err
	}
	nonce := base64.StdEncoding.EncodeToString(random)
	var quoted bytes.Buffer
	encoder := json.NewEncoder(&quoted)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(probeURL); err != nil {
		return err
	}
	href, err := nodeurl.ParseHTTPHref(probeURL)
	if err != nil {
		return err
	}
	body := `<h1>Sign in to ` + escapeHTML(server) + `</h1>
<p id="checking">Checking whether this browser runs on the p2 computer…</p>
<div id="choose" hidden>
<p>Where does this browser run?</p>
<p><a href="?via=loopback">On the p2 computer</a></p>
<p><a href="?via=callback">On another device</a></p>
</div>
<noscript><p><a href="?via=loopback">This browser runs on the p2 computer</a></p><p><a href="?via=callback">This browser runs on another device</a></p></noscript>
<script nonce="` + nonce + `">
let chosen = false;
const go = (via) => { if (!chosen) { chosen = true; location.replace("?via=" + via); } };
for (const link of document.querySelectorAll("#choose a")) link.addEventListener("click", () => { chosen = true; });
const slow = setTimeout(() => { document.getElementById("checking").hidden = true; document.getElementById("choose").hidden = false; }, 1500);
fetch(` + strings.TrimSuffix(quoted.String(), "\n") + `, { cache: "no-store" }).then(
	(answer) => { clearTimeout(slow); go(answer.ok ? "loopback" : "callback"); },
	() => { clearTimeout(slow); go("callback"); },
);
</script>`
	SendPage(response, http.StatusOK, document("Sign in to "+server, body), "script-src 'nonce-"+nonce+"'", "connect-src "+href.Origin)
	return nil
}

func document(title, body string) string {
	return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + escapeHTML(title) + `</title>
<style>body{font:16px/1.5 system-ui,sans-serif;max-width:32rem;margin:4rem auto;padding:0 1rem;color:#222}a{color:#0a58ca}@media(prefers-color-scheme:dark){body{background:#111;color:#ddd}a{color:#6ea8fe}}</style>
</head><body>` + body + `</body></html>`
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")

func escapeHTML(text string) string { return htmlEscaper.Replace(text) }
