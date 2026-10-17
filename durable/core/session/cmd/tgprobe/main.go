// Command tgprobe links the session layer's packages so the TinyGo build can prove they compile without reflection.
package main

import (
	"github.com/MichaelKinsy/PiG/durable/core/internal/delta"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
)

func main() {
	v, _ := jv.ParseString(`{"a":[1,2,"x"]}`)
	tr := delta.Track(v.(*jv.Object))
	ch := tr.BeginChange()
	_ = ch.State().Set("b", "y")
	p, _ := ch.Prepare()
	println(jv.Stringify(delta.OpsValue(p.Ops())), jv.Stringify(p.Value()))
}
