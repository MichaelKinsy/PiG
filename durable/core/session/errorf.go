// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session

import (
	"errors"
	"strconv"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

// sprintf formats the messages of the session's errors. The core may not import fmt (purity_test.go), and Pi's messages
// use only %d, %s and %v over integers, strings and errors, so those are the verbs this supports; %% is a percent sign.
// An argument of any other type is a programming error and panics.
func sprintf(format string, args ...any) string {
	out := make([]byte, 0, len(format)+16*len(args))
	next := 0
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' || i+1 == len(format) {
			out = append(out, c)
			continue
		}
		i++
		switch format[i] {
		case '%':
			out = append(out, '%')
		case 'd', 's', 'v':
			out = appendArg(out, args[next])
			next++
		default:
			panic("session: unsupported verb %" + string(format[i]))
		}
	}
	return string(out)
}

func appendArg(out []byte, arg any) []byte {
	switch v := arg.(type) {
	case string:
		return append(out, v...)
	case error:
		return append(out, v.Error()...)
	case int:
		return strconv.AppendInt(out, int64(v), 10)
	case int64:
		return strconv.AppendInt(out, v, 10)
	case float64:
		return append(out, jv.FormatNumber(v)...)
	case interface{ String() string }:
		return append(out, v.String()...)
	}
	panic("session: sprintf argument of an unsupported type")
}

// errorf is fmt.Errorf for the verbs sprintf supports.
func errorf(format string, args ...any) error { return errors.New(sprintf(format, args...)) }
