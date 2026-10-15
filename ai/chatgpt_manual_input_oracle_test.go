package ai

// pi: packages/ai/src/auth/oauth/openai-chatgpt.ts

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type chatgptManualProbe struct {
	Input string `json:"input"`
	State string `json:"state"`
}

type chatgptManualResult struct {
	Ok *struct {
		Code     string `json:"code"`
		ClientID string `json:"clientId"`
	} `json:"ok"`
	Err *string `json:"err"`
}

// chatgptManualProbes paste callback URLs the way a user would, and ways they go wrong: the right origin and path with codes, states and client ids of every shape (missing, empty, repeated, encoded, padded with JS whitespace), error
// parameters, other hosts, ports, schemes (ws, ftp, file, opaque ones such as "localhost:1455/..."), whitespace around the input, bare codes, relative references and text that does not parse.
func chatgptManualProbes() []chatgptManualProbe {
	random := rand.New(rand.NewSource(20260946))
	pad := []string{"", "", "", " ", "\t", "\n", "\ufeff", "\u0085", "\u00a0"}
	schemes := []string{"http://", "http://", "http://", "HTTP://", "https://", "ws://", "wss://", "ftp://", "file:///", "foo://", "", "//", "http:/", "http:\\\\", "localhost:"}
	hosts := []string{"127.0.0.1:1455", "127.0.0.1:1455", "127.0.0.1:1455", "127.1:1455", "0x7f.0.0.1:1455", "2130706433:1455", "127.0.0.1", "127.0.0.1:80", "127.0.0.1:1456", "localhost:1455", "LOCALHOST:1455", "u:p@127.0.0.1:1455", "127.0.0.1:01455", "127.0.0.1:1455.", "[::1]:1455", "127.0.0.1:99999", "127.0.0.1:", "", "ａ.example"}
	paths := []string{"/auth/callback", "/auth/callback", "/auth/callback", "/auth/callback/", "/auth/./callback", "/auth/../auth/callback", "/AUTH/callback", "/auth/callback%2F", "/auth/callbac%6b", "/x", "", "/", "\\auth\\callback"}
	queries := []string{
		"?code=abc&state=S&client_id=cid", "?code=abc&state=S&client_id=%20cid%20", "?state=S&client_id=cid", "?code=&state=S&client_id=cid", "?code=abc&client_id=cid", "?code=abc&state=&client_id=cid",
		"?code=abc&state=T&client_id=cid", "?code=abc&state=S", "?code=abc&state=S&client_id=", "?code=abc&state=S&client_id=%C2%A0", "?code=abc&state=S&client_id=%EF%BB%BF", "?error=access_denied&code=abc&state=S&client_id=cid",
		"?error=&code=abc&state=S&client_id=cid", "?code=a&code=b&state=S&client_id=c&client_id=d", "?code=a+b%2Bc&state=S&client_id=c", "?code=%E2%9C%93&state=S&client_id=c", "?code=%zz&state=S&client_id=c", "?code&state=S&client_id=c",
		"?error=x%20y&state=S", "?&&code=abc&&state=S&client_id=c", "", "?", "#code=abc&state=S", "?code=abc&state=S&client_id=cid#frag", "?code=abc;state=S&client_id=cid", "?%63ode=abc&state=S&client_id=cid",
		"?code=abc&state=%53&client_id=cid", "?code=abc&state=S\u00a0&client_id=cid",
	}
	states := []string{"S", "S", "S", "T", "", "s"}
	var probes []chatgptManualProbe
	for range 4500 {
		scheme, host, path := schemes[random.Intn(len(schemes))], hosts[random.Intn(len(hosts))], paths[random.Intn(len(paths))]
		if random.Intn(2) == 0 {
			scheme, host = "http://", "127.0.0.1:1455"
			if random.Intn(2) == 0 {
				path = "/auth/callback"
			}
		}
		input := pad[random.Intn(len(pad))] + scheme + host + path + queries[random.Intn(len(queries))] + pad[random.Intn(len(pad))]
		probes = append(probes, chatgptManualProbe{Input: input, State: states[random.Intn(len(states))]})
	}
	for _, input := range []string{"", " ", "abc", "abc123", "code=abc&state=S", "?code=abc&state=S", "/auth/callback?code=abc&state=S&client_id=c", "127.0.0.1:1455/auth/callback?code=abc", "localhost:1455/auth/callback?code=abc&state=S&client_id=c", "javascript:alert(1)", "mailto:a@b"} {
		probes = append(probes, chatgptManualProbe{Input: input, State: "S"})
	}
	return probes
}

// authorizationResultFromManualInput (with authorizationResultFromCallback and REDIRECT_URI, cut from Pi's source) runs in Node on the same pasted inputs: which inputs `new URL` rejects, the origin and pathname comparison with the
// redirect URI, the first value of each query parameter, the error parameter and the code, state and client id checks must produce Pi's error message or result in Pig's chatgptResultFromManualInput.
func TestChatGPTManualInputMatchesPiOnSeededProbes(t *testing.T) {
	probes := chatgptManualProbes()
	payload, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(testenv.ModuleRoot(t), ".upstream", "current", "packages", "ai", "src", "auth", "oauth", "openai-chatgpt.ts")
	cmd := exec.CommandContext(t.Context(), "node", "--disable-warning=ExperimentalWarning", "testdata/chatgpt_manual_input.mjs", source)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var expected []chatgptManualResult
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	if len(expected) != len(probes) {
		t.Fatalf("Pi answered %d of %d probes", len(expected), len(probes))
	}
	succeeded, originErrors, differing := 0, 0, 0
	for i, probe := range probes {
		result, err := chatgptResultFromManualInput(probe.Input, probe.State)
		got := chatgptManualResult{}
		if err != nil {
			message := err.Error()
			got.Err = &message
		} else {
			got.Ok = &struct {
				Code     string `json:"code"`
				ClientID string `json:"clientId"`
			}{result.code, result.clientID}
		}
		if expected[i].Ok != nil {
			succeeded++
		}
		if expected[i].Err != nil && len(*expected[i].Err) > 20 && (*expected[i].Err)[:20] == "The pasted callback " {
			originErrors++
		}
		want, _ := json.Marshal(expected[i])
		have, _ := json.Marshal(got)
		if !bytes.Equal(want, have) {
			if differing++; differing <= 12 {
				t.Errorf("%+v\n  Pig %s\n  Pi  %s", probe, have, want)
			}
		}
	}
	if differing > 12 {
		t.Errorf("%d of %d probes differ from Pi", differing, len(probes))
	}
	if succeeded < 200 || originErrors < 500 {
		t.Errorf("probes are one-sided: %d succeed, %d fail the origin check", succeeded, originErrors)
	}
}
