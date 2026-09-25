package ai

// Mirrors upstream .upstream/current/packages/ai/src/utils/oauth/oauth-page.ts.

import (
	"html"
	"strings"
)

const logoSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 800 800" aria-hidden="true"><path fill="#fff" fill-rule="evenodd" d="M165.29 165.29 H517.36 V400 H400 V517.36 H282.65 V634.72 H165.29 Z M282.65 282.65 V400 H400 V282.65 Z"/><path fill="#fff" d="M517.36 400 H634.72 V634.72 H517.36 Z"/></svg>`

func renderOAuthPage(title, heading, message, details string) string {
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>`)
	b.WriteString(html.EscapeString(title))
	b.WriteString(`</title>
  <style>
    :root {
      --text: #fafafa;
      --text-dim: #a1a1aa;
      --page-bg: #09090b;
      --font-sans: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, "Noto Sans", sans-serif;
      --font-mono: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
    }
    * { box-sizing: border-box; }
    html { color-scheme: dark; }
    body {
      margin: 0; min-height: 100vh; display: flex; align-items: center;
      justify-content: center; padding: 24px; background: var(--page-bg);
      color: var(--text); font-family: var(--font-sans); text-align: center;
    }
    main { width: 100%; max-width: 560px; display: flex; flex-direction: column; align-items: center; }
    .logo { width: 72px; height: 72px; display: block; margin-bottom: 24px; }
    h1 { margin: 0 0 10px; font-size: 28px; line-height: 1.15; font-weight: 650; }
    p { margin: 0; line-height: 1.7; color: var(--text-dim); font-size: 15px; }
    .details { margin-top: 16px; font-family: var(--font-mono); font-size: 13px; color: var(--text-dim); white-space: pre-wrap; word-break: break-word; }
  </style>
</head>
<body>
  <main>
    <div class="logo">`)
	b.WriteString(logoSVG)
	b.WriteString(`</div>
    <h1>`)
	b.WriteString(html.EscapeString(heading))
	b.WriteString(`</h1>
    <p>`)
	b.WriteString(html.EscapeString(message))
	b.WriteString(`</p>`)
	if details != "" {
		b.WriteString(`
    <div class="details">`)
		b.WriteString(html.EscapeString(details))
		b.WriteString(`</div>`)
	}
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
