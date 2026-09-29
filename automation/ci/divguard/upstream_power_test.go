package main

import (
	"fmt"
	"testing"
)

// A Go left shift and JavaScript exponentiation can spell the exact same timeout. This does not permit a different exponent or an expression that merely shares its prefix.
func TestMagicLiteralMatchesExactUpstreamPowerOfTwo(t *testing.T) {
	for _, tc := range []struct {
		source  string
		allowed bool
	}{{"2 ** 30", true}, {"2**3_0", true}, {"2 ** 29", false}, {"2 ** 300", false}, {"12 ** 30", false}, {"2 ** 30.5", false}} {
		t.Run(tc.source, func(t *testing.T) {
			environment := fixtureEnv(t)
			environment.Upstream.cache["packages/agent/src/mapped.ts"] = "function handleCtrlZ() { setInterval(() => {}, " + tc.source + "); }"
			code := []byte("package agent\nimport \"time\"\n// upstream: packages/agent/src/mapped.ts:handleCtrlZ\nvar suspendInterval = (1 << 30) * time.Millisecond\n")
			hits, problems, err := scanSource("agent/x.go", code, environment, []check{magicLiteral})
			if err != nil || len(problems) > 0 {
				t.Fatal(fmt.Sprint(err, problems))
			}
			if (len(hits) == 0) != tc.allowed {
				t.Fatalf("allowed=%v hits=%v", tc.allowed, hits)
			}
		})
	}
}
