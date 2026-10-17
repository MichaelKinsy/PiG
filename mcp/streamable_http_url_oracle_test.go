package mcp

import (
	"bufio"
	"context"
	"io"
	"net"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's StreamableHttpTransport constructor (transports/streamable-http.js) runs `new URL(options.url)`: it throws for an unparsable URL and keeps the WHATWG-serialized URL that every request then uses.
func TestStreamableHTTPTransportURLMatchesPiOracle(t *testing.T) {
	urls := []string{
		"https://mcp.example.com/mcp", "https://mcp.example.com", "HTTPS://MCP.EXAMPLE.COM:443/Mcp", "http://localhost:3000/mcp?x=a b#f", "https://exämple.com/é", "http://[0:0:0:0:0:0:0:1]:8080/", "http://127.1/", "http://0x7f.1:80/mcp",
		" https://mcp.example.com/mcp ", "https://mcp.example.com\\mcp", "https:\\\\mcp.example.com\\mcp", "https:/mcp.example.com/mcp", "https:mcp.example.com/mcp", "https://mcp.example.com/a/../b/./c", "https://u:p@mcp.example.com/mcp",
		"https://mcp.example.com/m\tcp", "https://mcp.example.com:99999/", "https://a b/", "https://", "https://[::1", "ws://h/x", "ftp://h:21/x", "file:///x", "custom://Host/Path", "custom:opaque", "mailto:a@b", "data:text/plain,x", "javascript:1", "a:b", "", "relative", "/relative", "//host/x", "http//x",
		"http://h/%zz", "http://h/a%2", "http://h/a|b", "http://h/a^b", "http://h/a[b]", "http://h/?q=%zz", "http://h/?q=a|b", "http://h/{x}",
	}
	var want []struct {
		Href  string `json:"href"`
		Error bool   `json:"error"`
	}
	pioracle.Run(t, `
const mod = await load("pi-mcp/transports/streamable-http.js");
emit(input.map((url) => {
	try { return { href: new mod.StreamableHttpTransport({ url }).url.href }; } catch { return { error: true }; }
}));`, urls, &want)
	for i, raw := range urls {
		transport, err := NewStreamableHTTPTransport(StreamableHTTPTransportOptions{URL: raw})
		switch {
		case err != nil && !want[i].Error:
			t.Errorf("url=%q: Go error %v, Pi %q", raw, err, want[i].Href)
		case err == nil && want[i].Error:
			t.Errorf("url=%q: Go accepted %q, Pi threw", raw, transport.URL)
		case err == nil && transport.URL.String() != want[i].Href:
			t.Errorf("url=%q: Go %q, Pi %q", raw, transport.URL.String(), want[i].Href)
		}
	}
}

// fetch(this.url) sends the WHATWG spelling without its fragment; http.NewRequest would reject "%zz" and re-escape "|".
func TestStreamableHTTPTransportSendsTheWHATWGRequestTarget(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	lines := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			lines <- err.Error()
			return
		}
		defer func() { _ = conn.Close() }()
		line, _ := bufio.NewReader(conn).ReadString('\n')
		lines <- line
		_, _ = io.WriteString(conn, "HTTP/1.1 401 Unauthorized\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
	}()
	origin := "http://" + listener.Addr().String()
	transport, err := NewStreamableHTTPTransport(StreamableHTTPTransportOptions{URL: origin + "/a|b/%zz?q=a|b#frag"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transport.Close() }()
	if err := transport.Start(); err != nil {
		t.Fatal(err)
	}
	_ = transport.Send(context.Background(), JSONRPCMessage{JSONRPC: "2.0", Method: "notifications/initialized"})
	_ = listener.Close()
	if line := <-lines; line != "POST "+origin+"/a|b/%zz?q=a|b HTTP/1.1\r\n" && line != "POST /a|b/%zz?q=a|b HTTP/1.1\r\n" {
		t.Fatalf("request line = %q", line)
	}
}
