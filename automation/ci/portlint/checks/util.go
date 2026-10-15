// SPDX-License-Identifier: MIT

package checks

import (
	"fmt"
	"strconv"
)

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

func unquote(s string) (string, error) { return strconv.Unquote(s) }
