package providerdiag

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture"
)

func Extension() *sdk.Extension { return testfixture.ProviderDiag() }
