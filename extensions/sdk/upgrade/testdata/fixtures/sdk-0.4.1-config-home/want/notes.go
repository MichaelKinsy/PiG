package subagent

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// announce compiles against the current SDK: the two results of ConfigHome
// spread into Println's ...any, which prints the error beside the path.
func announce(ctx sdk.Context) error {
	configHome, err := ctx.ConfigHome()
	if err != nil {
		return err
	}
	fmt.Println(configHome)
	return nil
}
