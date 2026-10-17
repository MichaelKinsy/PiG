package coding

import (
	"os"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

// Pi's mergeProviderAttributionHeaders (core/provider-attribution.js) chooses headers by `new URL(baseUrl).hostname` (OpenCode session pair, NVIDIA NIM, Cloudflare) and by substring (OpenRouter); the vendored Pi module runs over URL spellings the WHATWG parser normalizes differently from a byte comparison, and the set of header names must agree.
func TestProviderAttributionHostSelectionMatchesPiOracle(t *testing.T) {
	t.Setenv("PI_TELEMETRY", "")
	if err := os.Unsetenv("PI_TELEMETRY"); err != nil {
		t.Fatal(err)
	}
	hosts := []string{"opencode.ai", "integrate.api.nvidia.com", "api.cloudflare.com", "gateway.ai.cloudflare.com", "openrouter.ai"}
	var urls []string
	for _, host := range hosts {
		for _, form := range []string{
			"https://%s", "https://%s/v1", "https://%s:443/v1", "http://%s:8080", "HTTPS://%s", "https://%s.", "https://%s./v1", "https://sub.%s", "https://x.%s/", "https://%s.evil.test",
			"https://user:pw@%s/v1", "https://user@%s", "https://%s@evil.test", "https://evil.test/%s", "https://evil.test?h=%s", "https://evil.test#%s", "https://evil.test\\@%s",
			"//%s/v1", "%s", "%s/v1", " https://%s ", "\thttps://%s\n", "wss://%s/x", "ws://%s", "ftp://%s", "file://%s/x", "custom://%s/x", "HTTP://%s", "https:/%s", "https:%s", "https:///%s",
			"https://%s%%2f", "https://%s%%00", "https://%s;x", "https://%s,x", "https://%s:", "https://%s:0", "https://%s:99999", "https://%s:abc", "https://[%s]", "https://%s\u3002", "https://%s\uff0e",
			"https://\uff4f\uff50\uff45\uff4e\uff43\uff4f\uff44\uff45.ai", "https://xn--%s", "https://%s\u00a0", "https://%s/\u00e9", "https://%s?x=1", "https://%s#frag", "http://%s\\v1",
		} {
			urls = append(urls, sprintfURL(form, host))
		}
	}
	urls = append(urls, "", "not a url", "https://", "https://:443", "https://[::1]/", "https://127.0.0.1/", "https://0x7f.1/", "https://opencode.ai.", "https://OPENCODE.AI", "custom://OPENCODE.AI/x", "custom://OpenCode.ai/x", "web+x://API.Cloudflare.com/x", "CUSTOM://opencode.ai/x", "https://OpenRouter.AI/api", "https://myopenrouter.ai.example")
	type input struct {
		URLs []string `json:"urls"`
	}
	var want [][]string
	pioracle.Run(t, `
const mod = await load("pi-coding-agent/core/provider-attribution.js");
const settings = { getEnableInstallTelemetry: () => true };
emit(input.urls.map((baseUrl) => Object.keys(mod.mergeProviderAttributionHeaders({ provider: "custom", baseUrl }, settings, "session-1") ?? {}).sort()));`, input{urls}, &want)
	mismatches := 0
	for i, baseURL := range urls {
		got := []string{}
		for name := range mergeProviderAttributionHeaders("custom", baseURL, true, "session-1") {
			got = append(got, name)
		}
		slices.Sort(got)
		if want[i] == nil {
			want[i] = []string{}
		}
		if !slices.Equal(got, want[i]) {
			if mismatches++; mismatches <= 12 {
				t.Errorf("%q: Go %q, Pi %q", baseURL, got, want[i])
			}
		}
	}
	if mismatches > 12 {
		t.Errorf("%d mismatches in %d URLs", mismatches, len(urls))
	}
}

func sprintfURL(form, host string) string {
	out := make([]byte, 0, len(form)+len(host))
	for i := 0; i < len(form); i++ {
		switch {
		case form[i] == '%' && i+1 < len(form) && form[i+1] == 's':
			out = append(out, host...)
			i++
		case form[i] == '%' && i+1 < len(form) && form[i+1] == '%':
			out = append(out, '%')
			i++
		default:
			out = append(out, form[i])
		}
	}
	return string(out)
}
