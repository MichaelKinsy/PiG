package llama

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type hfProbe struct {
	Kind    string            `json:"kind"`
	Arg     string            `json:"arg"`
	Token   string            `json:"token"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

type hfOutcome struct {
	Err     string `json:"err"`
	Models  []any  `json:"models"`
	Details any    `json:"details"`
	Request string `json:"request"`
}

// The Hugging Face client (llama/huggingface.js) against Pi over a local server: the query-string and path encoding of searches and model ids, the bearer header, error text for every status (rate-limit delays from retry-after and ratelimit headers, error payloads, non-JSON bodies), search results with
// malformed entries, and model details with GGUF siblings: quantization detection (case folding, shard suffixes, mmproj files), size sums with unknown sizes, the Q4_K_M-first order and the localeCompare tie-break.
func TestHuggingFaceClientMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(31))
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	quant := []string{"IQ2_X_S", "IQ2_XS", "IQ2_X5", "Q4_K_M", "q4_k_m", "Q8_0", "IQ2_XXS", "UD-Q2_K_XL", "BF16", "bf16", "F16", "F32", "MXFP4", "MXFP4_MOE", "Q5_K_S", "Q6_K", "IQ3_M", "ſ", "Q4_K_ſ", "Q4_K_\u212a", "Q4", "Q_4", "Q4_", "x", ""}
	file := func() string {
		name := pick("model", "Llama-3", "m", "日本-model", "mmproj", "MMPROJ-x", "a.b")
		sep := pick("-", "_", ".", "", "x")
		ext := pick(".gguf", ".GGUF", ".gguf", ".bin", ".gguf.bak", "gguf")
		shard := pick("", "", "-00001-of-00002", "-00002-of-00002", "-0001-of-0002")
		dir := pick("", "", "sub/", "a/b/", "mmproj/")
		return dir + name + sep + pick(quant...) + shard + ext
	}
	sibling := func() string {
		switch rng.Intn(10) {
		case 0:
			return pick(`null`, `5`, `"x"`, `[]`, `{}`, `{"rfilename":5}`, `{"size":3}`)
		case 1:
			return fmt.Sprintf(`{"rfilename":%q}`, file())
		}
		return fmt.Sprintf(`{"rfilename":%q,"size":%s}`, file(), pick("null", "null", "100", "5", "1e3", "0", "1.5", "9007199254740993", "1e21", `"7"`, "null", "100", "100"))
	}
	var probes []hfProbe
	for range 1500 {
		p := hfProbe{Token: pick("", "", "tok", "t o k"), Status: 200, Headers: map[string]string{}}
		if rng.Intn(2) == 0 {
			p.Kind = "search"
			p.Arg = pick("", "llama", "a b", "a+b", "a&b=c", "日本", "q~*'()!", "x/y", "100%", "é", " ", "a\nb")
		} else {
			p.Kind = "details"
			p.Arg = pick("org/model", "a b/c d", "日本/モデル", "x", "", "a//b", "/lead", "trail/", "q?x=1/y#z", "é/ü", "a+b/c%d", "~*'()!/-_.")
		}
		switch rng.Intn(10) {
		case 0:
			p.Status = pick2(rng, 404, 500, 401, 503, 400)
			p.Body = pick(`{"error":"nope"}`, `{"error":""}`, `{"error":5}`, `oops`, ``, `null`, `[]`, `{"error":"multi\nline"}`)
		case 1:
			p.Status = 429
			if rng.Intn(2) == 0 {
				p.Headers["Retry-After"] = pick("5", "0", "abc", "1e2", " 7 ", "", "-3", "0x10", "1.5", "99999999999999999999", "Infinity")
			}
			if rng.Intn(2) == 0 {
				p.Headers["Ratelimit"] = pick("t=30", "a=1;t=12", "t=x", "x;t=7;t=9", "", "t=007", "\"default\";r=0;t=45", "t=99999999999999999999999")
			}
			p.Body = pick(`{"error":"slow"}`, `x`)
		default:
			if p.Kind == "search" {
				items := []string{}
				for range rng.Intn(5) {
					items = append(items, pick(`{"id":"a/b","downloads":5}`, `{"id":"c","downloads":"5"}`, `{"id":"d"}`, `{"id":5}`, `null`, `"s"`, `[]`, `{"downloads":1}`, `{"id":"é","downloads":1e21}`, `{"id":"neg","downloads":-3}`))
				}
				p.Body = "[" + strings.Join(items, ",") + "]"
				if rng.Intn(6) == 0 {
					p.Body = pick(`{}`, `null`, `"x"`, `5`, `{"models":[]}`, `not json`, ``)
				}
			} else {
				siblings := []string{}
				for range rng.Intn(8) {
					siblings = append(siblings, sibling())
				}
				members := []string{"\"siblings\":[" + strings.Join(siblings, ",") + "]"}
				if rng.Intn(2) == 0 {
					members = append(members, fmt.Sprintf(`"id":%s`, pick(`"canon/name"`, `5`, `null`, `""`)))
				}
				if rng.Intn(2) == 0 {
					members = append(members, fmt.Sprintf(`"gated":%s`, pick(`"auto"`, `"manual"`, `false`, `true`, `"other"`, `"AUTO"`)))
				}
				p.Body = "{" + strings.Join(members, ",") + "}"
				if rng.Intn(10) == 0 {
					p.Body = pick(`[]`, `null`, `"x"`, `5`, `not json`, `{"siblings":5}`, `{"siblings":{"a":1}}`)
				}
			}
		}
		probes = append(probes, p)
	}
	// Equal sizes (or none) fall to localeCompare, which sorts "_" before letters where byte order does not.
	for _, body := range []string{
		`{"siblings":[{"rfilename":"m-IQ2_XS.gguf"},{"rfilename":"m-IQ2_X_S.gguf"}]}`,
		`{"siblings":[{"rfilename":"m-IQ2_XS.gguf","size":5},{"rfilename":"m-IQ2_X_S.gguf","size":5},{"rfilename":"m-IQ2_X5.gguf","size":5}]}`,
		`{"siblings":[{"rfilename":"m-IQ2_X5.gguf"},{"rfilename":"m-IQ2_XS.gguf"},{"rfilename":"m-IQ2_X_S.gguf"},{"rfilename":"m-Q4_K_M.gguf"}]}`,
	} {
		probes = append(probes, hfProbe{Kind: "details", Arg: "o/m", Status: 200, Headers: map[string]string{}, Body: body})
	}
	var mu sync.Mutex
	index := 0
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		p := probes[index]
		index++
		requests = append(requests, r.RequestURI+" auth="+r.Header.Get("Authorization"))
		mu.Unlock()
		for k, v := range p.Headers {
			w.Header()[k] = []string{v}
		}
		w.WriteHeader(p.Status)
		_, _ = w.Write([]byte(p.Body))
	}))
	defer server.Close()
	type input struct {
		URL    string    `json:"url"`
		Probes []hfProbe `json:"probes"`
	}
	var want []hfOutcome
	pioracle.Run(t, `
const { HuggingFaceClient } = await load("pi-coding-agent/extensions/llama/huggingface.js");
const out = [];
for (const p of input.probes) {
	const client = new HuggingFaceClient(p.token || undefined, input.url + "///");
	try {
		if (p.kind === "search") out.push({ models: await client.search(p.arg) });
		else out.push({ details: await client.details(p.arg) });
	} catch (e) { out.push({ err: e instanceof Error ? e.message : String(e) }); }
}
emit(out);
`, input{URL: server.URL, Probes: probes}, &want)
	mu.Lock()
	piRequests := requests
	index, requests = 0, nil
	mu.Unlock()
	bad := 0
	for i, p := range probes {
		client := NewHuggingFaceClient(p.Token, server.URL+"///")
		var got hfOutcome
		if p.Kind == "search" {
			models, err := client.Search(t.Context(), p.Arg)
			if err != nil {
				got.Err = err.Error()
			} else {
				for _, m := range models {
					got.Models = append(got.Models, map[string]any{"id": m.ID, "downloads": m.Downloads})
				}
			}
		} else {
			details, err := client.Details(t.Context(), p.Arg)
			if err != nil {
				got.Err = err.Error()
			} else {
				quantizations := []any{}
				for _, q := range details.Quantizations {
					entry := map[string]any{"name": q.Name}
					if q.Size != nil {
						entry["size"] = *q.Size
					}
					quantizations = append(quantizations, entry)
				}
				gated := any(false)
				if details.Gated != "" {
					gated = string(details.Gated)
				}
				got.Details = map[string]any{"id": details.ID, "gated": gated, "quantizations": quantizations}
			}
		}
		encoded, _ := json.Marshal(got)
		var gotAny, wantAny map[string]any
		_ = json.Unmarshal(encoded, &gotAny)
		wantEncoded, _ := json.Marshal(want[i])
		_ = json.Unmarshal(wantEncoded, &wantAny)
		normalize := func(m map[string]any) {
			delete(m, "request")
			if models, ok := m["models"].([]any); ok && len(models) == 0 {
				delete(m, "models")
			}
			if m["models"] == nil {
				delete(m, "models")
			}
			if m["details"] == nil {
				delete(m, "details")
			}
			if m["err"] == "" {
				delete(m, "err")
			}
		}
		normalize(gotAny)
		normalize(wantAny)
		goRequest := ""
		mu.Lock()
		goRequest = requests[i]
		mu.Unlock()
		if !reflect.DeepEqual(gotAny, wantAny) || goRequest != piRequests[i] {
			if bad++; bad <= 5 {
				t.Errorf("probe %d %+v:\n got %s\nwant %s\n go request %s\n pi request %s", i, p, encoded, wantEncoded, goRequest, piRequests[i])
			}
		}
	}
	if bad > 5 {
		t.Errorf("%d of %d probes differ", bad, len(probes))
	}
}

func pick2(rng *rand.Rand, values ...int) int { return values[rng.Intn(len(values))] }
