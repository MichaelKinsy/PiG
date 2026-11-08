package runtime

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
)

// Ports apps/p2/src/runtime/page.ts: server-rendered pages (join, owner sign-in, ended share, UI-less server) use the
// app's visual language: the tokens of src/web/style.css, thin separators, an underlined input, one outline button and
// the canonical colored Pi logo. Without a script they follow the system scheme; with a nonce they also apply the
// browser's sidebar theme choice.

const pageStyle = `:root{color-scheme:light dark;--bg:#fbfaf8;--text:#1d1b19;--muted:#77716a;--line:#e7e4df;--hover:#f1eee9;--bad:#c8433d;font:16px/1.5 -apple-system,BlinkMacSystemFont,"Inter","Segoe UI",Roboto,sans-serif;background:var(--bg);color:var(--text);-webkit-font-smoothing:antialiased;-webkit-text-size-adjust:100%;text-size-adjust:100%}
@media (prefers-color-scheme:dark){:root:not([data-theme=light]){color-scheme:dark;--bg:#151413;--text:#ecebe8;--muted:#9b958d;--line:#2c2a27;--hover:#22201e;--bad:#f07a72}}
:root[data-theme=dark]{color-scheme:dark;--bg:#151413;--text:#ecebe8;--muted:#9b958d;--line:#2c2a27;--hover:#22201e;--bad:#f07a72}
:root[data-theme=light]{color-scheme:light}
*{box-sizing:border-box}body{margin:0}
main{display:flex;flex-direction:column;gap:16px;max-width:440px;margin:0 auto;padding:12vh 16px 24px}
header{display:flex;align-items:center;gap:12px}
header svg{flex:none;width:32px;height:32px;shape-rendering:crispEdges}
h1{margin:0;font-size:22px;font-weight:600;letter-spacing:-.02em;overflow-wrap:anywhere}
p{margin:0;color:var(--muted);font-size:15px}
.small{font-size:13px}
form{display:flex;flex-direction:column;gap:12px}
label{margin-bottom:-8px;color:var(--muted);font-size:11px;font-weight:600;letter-spacing:.04em;text-transform:uppercase}
input{width:100%;height:44px;padding:0;border:0;border-bottom:1px solid var(--line);border-radius:0;outline:0;background:transparent;color:var(--text);font:inherit;font-size:15px}
input:focus{border-bottom-color:var(--text)}
.mono{font-family:ui-monospace,"SF Mono","JetBrains Mono","Cascadia Code",Menlo,monospace}
button{align-self:flex-start;min-height:40px;padding:0 16px;border:1px solid var(--text);border-radius:4px;background:transparent;color:var(--text);font:inherit;font-size:13px;font-weight:600;cursor:pointer}
button:hover{background:var(--hover)}button:disabled{opacity:.45;cursor:default}
:focus-visible{outline:1px solid var(--text);outline-offset:2px}
.error{color:var(--bad)}`

// pageLogo has the same geometry and colours as src/web/PiLogo.tsx (the canonical startup logo).
const pageLogo = `<svg viewBox="0 0 4 4" role="img" aria-label="Pi"><rect fill="rgb(228 138 122)" width="3" height="1"/><rect fill="rgb(228 138 122)" x="2" y="1" width="1" height="1"/><rect fill="rgb(79 142 179)" y="1" width="1" height="3"/><rect fill="rgb(79 142 179)" x="1" y="2" width="1" height="1"/><rect fill="rgb(234 182 93)" x="3" y="2" width="1" height="2"/></svg>`

const pageTheme = `try{const t=localStorage.getItem("pi-theme");if(t==="light"||t==="dark")document.documentElement.dataset.theme=t}catch{}`

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

// PageOptions is what Page renders: Heading is escaped; Body and Script are trusted markup the server wrote. The nonce
// enables the theme script and Script.
type PageOptions struct {
	Title, Heading, Body, Script, Nonce string
}

// Page is a server-rendered page.
func Page(options PageOptions) string {
	theme, script := "", ""
	if options.Nonce != "" {
		theme = fmt.Sprintf(`<script nonce="%s">%s</script>`, options.Nonce, pageTheme)
		if options.Script != "" {
			script = fmt.Sprintf(`<script nonce="%s">%s</script>`, options.Nonce, options.Script)
		}
	}
	return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light dark"><title>` +
		htmlEscaper.Replace(options.Title) + `</title><style>` + pageStyle + `</style>` + theme + `</head><body><main><header>` + pageLogo +
		`<h1>` + htmlEscaper.Replace(options.Heading) + `</h1></header>` + options.Body + `</main>` + script + `</body></html>`
}

const signInBody = `<p>Run <span class="mono">npm run owner-link</span> on the host and open its link on this device. You can also paste a one-time token from Devices.</p>
<form id="sign-in"><label for="token">One-time token</label><input id="token" class="mono" type="password" autocomplete="off" spellcheck="false" required><button id="submit" type="submit">Sign in</button></form>
<p id="status" role="status"></p><p class="small">This grants full owner access, including shell commands. Tokens expire after ten minutes and work once.</p>`

const signInScript = `const input=document.getElementById("token"),button=document.getElementById("submit"),status=document.getElementById("status");
async function claim(token){button.disabled=true;status.className="";status.textContent="Signing in…";try{
 const r=await fetch("/owner/claim",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({token})});
 if(!r.ok)throw new Error("Invalid, expired or already-used link. Generate a fresh owner link on the host.");input.value="";location.replace("/");
}catch(e){status.className="error";status.textContent=e.message;button.disabled=false;}}
document.getElementById("sign-in").onsubmit=(event)=>{event.preventDefault();claim(input.value.trim());};
if(location.hash){const fragment=location.hash.slice(1);history.replaceState(null,"","/owner");try{claim(decodeURIComponent(fragment));}catch{status.className="error";status.textContent="Invalid owner link.";}}`

// SignIn answers with the public, credential-free landing page. An invitation is only ever submitted in a same-origin
// POST.
func SignIn(response http.ResponseWriter, request *http.Request, status int) {
	random := make([]byte, 18)
	// crypto/rand does not fail on the supported systems (it terminates the program if it would).
	_, _ = rand.Read(random)
	nonce := base64.RawURLEncoding.EncodeToString(random)
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
	response.WriteHeader(status)
	if request.Method == http.MethodHead {
		return
	}
	_, _ = response.Write([]byte(Page(PageOptions{Title: "Pi · Sign in", Heading: "Sign in to Pi", Nonce: nonce, Body: signInBody, Script: signInScript})))
}
