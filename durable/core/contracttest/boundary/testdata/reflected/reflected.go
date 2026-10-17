// Package reflected is a deliberate violation: a core package must not import reflect.
package reflected

import "reflect"

func Kind(v any) reflect.Kind { return reflect.TypeOf(v).Kind() }
