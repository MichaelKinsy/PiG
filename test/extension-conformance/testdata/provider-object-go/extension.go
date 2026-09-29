package providerobject

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/providerobject"
)

func Extension() *sdk.Extension { return providerobject.Extension() }
