package llama

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type llamaHTTPProbe struct {
	Op      string `json:"op"`
	Arg     string `json:"arg"`
	Reload  bool   `json:"reload"`
	APIKey  string `json:"apiKey"`
	Status  int    `json:"status"`
	Body    string `json:"body"`
	Request string `json:"-"`
}

// The router-mode requests of LlamaClient (llama/client.js) against Pi over a local server: list (with and without reload), props (query encoding of the model), load, unload and download bodies and headers, the bearer header, error text for every status and error payload shape, and the
// catalog and props validation of malformed payloads. Requests (method, target, headers, body) and results are compared.
func TestLlamaClientRequestsMatchPi(t *testing.T) {
	rng := rand.New(rand.NewSource(57))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	entry := func() string {
		return pick(`{"id":"a","status":{"value":"loaded"}}`, `{"id":"b/c:Q4","status":{"value":"unloaded","args":["x"]}}`, `{"id":"日本","status":{"value":"loading"}}`, `{"id":"x","status":{"value":5}}`, `{"id":5,"status":{"value":"loaded"}}`, `{"id":"nostatus"}`, `null`, `"s"`, `[]`, `{"id":"d","status":null}`, `{"id":"e","status":{"value":"sleeping"},"meta":{"n_ctx":4096}}`)
	}
	var probes []llamaHTTPProbe
	for range 1200 {
		p := llamaHTTPProbe{Op: pick("list", "props", "load", "unload", "download"), APIKey: pick("", "", "key", "k e y"), Status: 200}
		p.Arg = pick("", "m", "a b", "x/y:Q4_K_M", "日本", "q&r=s+t", "<tag>&'\"", "\u2028x", "~*'()!", "100%")
		p.Reload = rng.Intn(2) == 0
		switch rng.Intn(8) {
		case 0:
			p.Status = []int{400, 404, 500, 401, 503}[rng.Intn(5)]
			p.Body = pick(`{"error":{"message":"bad model"}}`, `{"error":{"message":""}}`, `{"error":{"message":5}}`, `{"error":"flat"}`, `{"error":null}`, `{"error":[]}`, `oops`, ``, `null`, `[]`, `{"error":{}}`)
		default:
			switch p.Op {
			case "list":
				items := []string{}
				for range rng.Intn(4) {
					items = append(items, entry())
				}
				p.Body = `{"data":[` + strings.Join(items, ",") + `]}`
				if rng.Intn(6) == 0 {
					p.Body = pick(`{}`, `{"data":{}}`, `{"data":null}`, `[]`, `null`, `"x"`, `not json`, ``, `{"data":"x"}`, `{"data":[]}`)
				}
			case "props":
				members := []string{}
				if rng.Intn(2) == 0 {
					members = append(members, `"models_autoload":`+pick("true", "false", `"true"`, "1", "null"))
				}
				if rng.Intn(2) == 0 {
					members = append(members, `"chat_template":`+pick(`"{{ x }}"`, `""`, `5`, `null`))
				}
				p.Body = "{" + strings.Join(members, ",") + "}"
				if rng.Intn(6) == 0 {
					p.Body = pick(`[]`, `null`, `"x"`, `5`, `not json`, ``)
				}
			default:
				p.Body = pick(`{"success":true}`, ``, `not json`, `[]`)
			}
		}
		probes = append(probes, p)
	}
	var mu sync.Mutex
	index := 0
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		p := probes[index]
		index++
		requests = append(requests, fmt.Sprintf("%s %s auth=%q type=%q body=%q", r.Method, r.RequestURI, r.Header.Get("Authorization"), r.Header.Get("Content-Type"), body))
		mu.Unlock()
		w.WriteHeader(p.Status)
		_, _ = w.Write([]byte(p.Body))
	}))
	defer server.Close()
	type outcome struct {
		Err   string   `json:"err"`
		IDs   []string `json:"ids"`
		Props any      `json:"props"`
	}
	var want []outcome
	pioracle.Run(t, `
const { LlamaClient } = await load("pi-coding-agent/extensions/llama/client.js");
const out = [];
for (const p of input.probes) {
	const client = new LlamaClient(input.url + "/v1/", p.apiKey || undefined);
	try {
		if (p.op === "list") out.push({ ids: (await client.list({ reload: p.reload })).map((m) => m.id + "|" + m.status.value) });
		else if (p.op === "props") out.push({ props: await client.props({ model: p.arg || undefined }) });
		else { await client[p.op](p.arg); out.push({}); }
	} catch (e) { out.push({ err: e instanceof Error ? e.message : String(e) }); }
}
emit(out);
`, struct {
		URL    string           `json:"url"`
		Probes []llamaHTTPProbe `json:"probes"`
	}{server.URL, probes}, &want)
	mu.Lock()
	piRequests := requests
	index, requests = 0, nil
	mu.Unlock()
	bad := 0
	for i, p := range probes {
		client, err := NewLlamaClient(server.URL+"/v1/", p.APIKey)
		if err != nil {
			t.Fatal(err)
		}
		var got outcome
		var callErr error
		switch p.Op {
		case "list":
			var models []LlamaModelInfo
			if models, callErr = client.List(t.Context(), p.Reload); callErr == nil {
				got.IDs = []string{}
				for _, m := range models {
					got.IDs = append(got.IDs, m.ID+"|"+string(m.Status.Value))
				}
			}
		case "props":
			var props LlamaServerProps
			if props, callErr = client.Props(t.Context(), p.Arg); callErr == nil {
				got.Props = props
			}
		case "load":
			callErr = client.Load(t.Context(), p.Arg)
		case "unload":
			callErr = client.Unload(t.Context(), p.Arg)
		case "download":
			callErr = client.Download(t.Context(), p.Arg)
		}
		if callErr != nil {
			got.Err = callErr.Error()
		}
		mu.Lock()
		goRequest := requests[i]
		mu.Unlock()
		w := want[i]
		same := got.Err == w.Err && reflect.DeepEqual(got.IDs, w.IDs) && goRequest == piRequests[i]
		if same && p.Op == "props" && got.Err == "" {
			props := got.Props.(LlamaServerProps)
			wantProps, _ := w.Props.(map[string]any)
			_, hasAutoload := wantProps["models_autoload"]
			template, hasTemplate := wantProps["chat_template"]
			same = (!hasAutoload || wantProps["models_autoload"] == props.ModelsAutoload) && (hasAutoload || !props.ModelsAutoload) && (!hasTemplate && props.ChatTemplate == "" || hasTemplate && template == props.ChatTemplate)
		}
		if !same {
			if bad++; bad <= 5 {
				encoded, _ := json.Marshal(got)
				wantEncoded, _ := json.Marshal(w)
				t.Errorf("probe %d %+v:\n got %s\nwant %s\n go request %s\n pi request %s", i, p, encoded, wantEncoded, goRequest, piRequests[i])
			}
		}
	}
	if bad > 5 {
		t.Errorf("%d of %d probes differ", bad, len(probes))
	}
}
