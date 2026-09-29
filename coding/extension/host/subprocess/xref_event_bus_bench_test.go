package subprocess

import (
	"encoding/json"
	"testing"
)

// BenchmarkXrefEventBusEmit measures one emit whose listener reads and writes the payload: in-heap for one realm, and through the Host registry and xref operations across two realms.
func BenchmarkXrefEventBusEmit(b *testing.B) {
	const emits = 200
	sources := []string{
		`export default pi=>{` + xrefTool("burst", `const data={n:0};const start=performance.now();for(let i=0;i<200;i++)pi.events.emit("tick",data);return {content:[{type:"text",text:String(performance.now()-start)}]};`) + `};`,
		`export default pi=>{pi.events.on("tick",data=>{data.n=data.n+1;});};`,
	}
	for _, topology := range []struct {
		name      string
		isolation []string
	}{{"one-realm", []string{"", ""}}, {"two-realms", []string{"isolated", "isolated"}}} {
		b.Run(topology.name, func(b *testing.B) {
			_, exts := xrefFixtureB(b, topology.isolation, sources...)
			tool := exts["xref-0"].Tools["burst"]
			b.ResetTimer()
			for range b.N {
				result, err := tool.Definition.Execute(b.Context(), "bench", json.RawMessage(`{}`), nil)
				if err != nil {
					b.Fatal(err)
				}
				_ = result
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*emits), "ns/emit")
		})
	}
}
