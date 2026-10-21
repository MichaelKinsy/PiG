package childsession

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/childsession"
)

func Extension() *sdk.Extension { return childsession.Extension() }
