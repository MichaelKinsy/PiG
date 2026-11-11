package http

import nethttp "net/http"

// nodeStatusText are the reason phrases where Node's `STATUS_CODES` (`node:http`) differs from Go's net/http.StatusText: the
// component's own error answers write the reason phrase as their text, as Pi's do. Go-only file.
var nodeStatusText = map[int]string{
	413: "Payload Too Large",
	414: "URI Too Long",
	416: "Range Not Satisfiable",
	418: "I'm a Teapot",
	509: "Bandwidth Limit Exceeded",
}

// statusText is Node's `STATUS_CODES[status]` ("" for a status it has no phrase for).
func statusText(status int) string {
	if text, ok := nodeStatusText[status]; ok {
		return text
	}
	return nethttp.StatusText(status)
}
