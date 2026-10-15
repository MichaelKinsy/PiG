package erroridentity

import (
	"errors"
	"strings"
)

var errX = errors.New("x")

func bad(err error) bool {
	return err.Error() == "usage limit" // want `comparing err.Error\(\) text`
}

func badContains(err error) bool {
	return strings.Contains(err.Error(), "nothing to compact") // want `strings.Contains on err.Error\(\)`
}

func good(err error) bool { return errors.Is(err, errX) }
