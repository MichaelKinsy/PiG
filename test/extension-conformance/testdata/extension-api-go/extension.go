package extensionapi

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/extensionapi"
)

func Extension() *sdk.Extension { return extensionapi.Extension() }
