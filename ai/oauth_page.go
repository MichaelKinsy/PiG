package ai

// Ports packages/ai/src/utils/oauth-page.ts.

import (
	"html"
	"strings"
)

const logoSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 800 800" aria-hidden="true"><path fill="#F09082" d="M165.29 165.29H517.36V400H400V282.65H165.29Z"/><path fill="#4D9ABF" d="M165.29 282.65H282.65V400H400V517.36H282.65V634.72H165.29Z"/><path fill="#F1BE58" d="M517.36 400H634.72V634.72H517.36Z"/></svg>`

// escapeOAuthPageHTML is Pi's escapeHtml: & < > " ' become &amp; &lt; &gt; &quot; &#39;. html.EscapeString escapes the same five characters and spells the double quote &#34;, which only a double quote can produce in its output.
func escapeOAuthPageHTML(value string) string {
	return strings.ReplaceAll(html.EscapeString(value), "&#34;", "&quot;")
}

// renderOAuthPage is Pi's renderPage template; every interpolated value is escaped and empty details omit the details block.
func renderOAuthPage(title, heading, message, details string) string {
	detailsHTML := ""
	if details != "" {
		detailsHTML = `<div class="details">` + escapeOAuthPageHTML(details) + `</div>`
	}
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>`)
	b.WriteString(escapeOAuthPageHTML(title))
	b.WriteString(`</title>
  <style>
    :root {
      --text: #fafafa;
      --text-dim: #a1a1aa;
      --page-bg: #09090b;
      --font-sans: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, "Noto Sans", sans-serif, "Apple Color Emoji", "Segoe UI Emoji", "Segoe UI Symbol", "Noto Color Emoji";
      --font-mono: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
    }
    * { box-sizing: border-box; }
    html { color-scheme: dark; }
    body {
      margin: 0;
      min-height: 100vh;
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 24px;
      background: var(--page-bg);
      color: var(--text);
      font-family: var(--font-sans);
      text-align: center;
    }
    main {
      width: 100%;
      max-width: 560px;
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
    }
    .logo {
      width: 72px;
      height: 72px;
      display: block;
      margin-bottom: 24px;
    }
    h1 {
      margin: 0 0 10px;
      font-size: 28px;
      line-height: 1.15;
      font-weight: 650;
      color: var(--text);
    }
    p {
      margin: 0;
      line-height: 1.7;
      color: var(--text-dim);
      font-size: 15px;
    }
    .details {
      margin-top: 16px;
      font-family: var(--font-mono);
      font-size: 13px;
      color: var(--text-dim);
      white-space: pre-wrap;
      word-break: break-word;
    }
  </style>
</head>
<body>
  <main>
    <div class="logo">`)
	b.WriteString(logoSVG)
	b.WriteString(`</div>
    <h1>`)
	b.WriteString(escapeOAuthPageHTML(heading))
	b.WriteString(`</h1>
    <p>`)
	b.WriteString(escapeOAuthPageHTML(message))
	b.WriteString(`</p>
    `)
	b.WriteString(detailsHTML)
	b.WriteString(`
  </main>
</body>
</html>`)
	return b.String()
}

// OAuthSuccessHTML returns the HTML page shown on successful OAuth callback.
func OAuthSuccessHTML(message string) string {
	return renderOAuthPage("Authentication successful", "Authentication successful", message, "")
}

// OAuthErrorHTML returns the HTML page shown on OAuth callback error.
func OAuthErrorHTML(message, details string) string {
	return renderOAuthPage("Authentication failed", "Authentication failed", message, details)
}
