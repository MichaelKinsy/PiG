package contracts_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

func TestNormalFormMatchesFingerprint(t *testing.T) {
	c := remote(t, "n", 1, props(contracts.Prop("id", contracts.Str())),
		rstate(contracts.StateEntry("a", contracts.Str()), contracts.StateEntry("b", contracts.NewGrantable(contracts.Int()))),
	)
	data := dataOf(t, c)
	current := data.Versions[len(data.Versions)-1]
	normal := contracts.NormalForm(data.Kind, data.Params, current.Members, []string{"b"}, nil)
	fp := must(contracts.FingerprintOf(normal))
	if fp != c.Fingerprint {
		t.Fatalf("FingerprintOf(NormalForm) = %s, want %s", fp, c.Fingerprint)
	}
}
