package sessiongraph

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/sessiongraph"
)

func Extension() *sdk.Extension { return sessiongraph.Extension() }
