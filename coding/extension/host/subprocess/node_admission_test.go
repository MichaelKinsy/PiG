package subprocess

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

// Pi loader.ts awaits factories in configured order while resource-loader.ts hands them one bus. A native factory between two Node factories must not split their bus.
func TestNodeCellInterleavedGoFactoryKeepsOrderAndBus(t *testing.T) {
	nodeCellRequireNode(t)
	root := t.TempDir()
	trace := filepath.Join(root, "order")
	write := func(name, source string) string {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	a := write("a.mjs", fmt.Sprintf(`import {appendFileSync} from "node:fs";
export default function(pi) {
 appendFileSync(%q,"A\n");
 pi.events.on("ping", x => { x.a = true; });
 pi.registerTool({name:"ask",label:"ask",description:"ask",parameters:{type:"object",properties:{}},execute:async()=>{
 const x={}; pi.events.emit("ping",x); return {content:[{type:"text",text:JSON.stringify(x)}]}; }});
}`, trace))
	c := write("c.mjs", fmt.Sprintf(`import {appendFileSync,readFileSync} from "node:fs";
export default function(pi) {
 if (readFileSync(%q,"utf8") !== "A\nB\n") throw new Error("C admitted out of order");
 appendFileSync(%q,"C\n"); pi.events.on("ping", x => {x.c = true;});
}`, trace, trace))
	goRoot := filepath.Join(root, "native")
	if err := os.Mkdir(goRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	sdk, err := filepath.Abs("../../../../extensions/sdk")
	if err != nil {
		t.Fatal(err)
	}
	mod := fmt.Sprintf("module example.com/admission\n\ngo 1.26.0\n\nrequire github.com/MichaelKinsy/PiG/extensions/sdk v%s\nreplace github.com/MichaelKinsy/PiG/extensions/sdk => %s\n", pigversion.PigVersion, filepath.ToSlash(sdk))
	if err := os.WriteFile(filepath.Join(goRoot, "go.mod"), []byte(mod), 0o644); err != nil {
		t.Fatal(err)
	}
	source := fmt.Sprintf(`package admission
import ("os"; sdk "github.com/MichaelKinsy/PiG/extensions/sdk")
func Extension() *sdk.Extension {
 b,err:=os.ReadFile(%q); if err!=nil || string(b)!="A\n" {panic("B admitted out of order")}
 f,err:=os.OpenFile(%q,os.O_APPEND|os.O_WRONLY,0600); if err!=nil {panic(err)}
 if _,err=f.WriteString("B\n");err!=nil{panic(err)}; if err=f.Close();err!=nil{panic(err)}
 return sdk.New("native")
}`, trace, trace)
	if err := os.WriteFile(filepath.Join(goRoot, "extension.go"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgs := []ExtConfig{{Name: "a", Source: a, Enabled: true}, packedFactoryConfig("native", goRoot, "example.com/admission", "native"), {Name: "c", Source: c, Enabled: true}}
	h := NewHost(t.TempDir())
	t.Cleanup(func() { h.Shutdown("test done") })
	h.SetConfigLoader(func() ([]ExtConfig, error) { return cfgs, nil })
	for _, reload := range []bool{false, true} {
		if err := os.WriteFile(trace, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if reload {
			if _, err := h.Reload(t.Context()); err != nil {
				t.Fatal(err)
			}
		} else {
			if _, errs := h.LoadAll(t.Context(), cfgs); len(errs) > 0 {
				t.Fatal(errs)
			}
		}
		h.mu.Lock()
		ma, mc := h.exts["a"], h.exts["c"]
		h.mu.Unlock()
		if ma == nil || mc == nil {
			t.Fatalf("missing admitted members: %v", h.LoadErrors())
		}
		if ma.proc.Pid != mc.proc.Pid {
			t.Errorf("interleaved Node factories split into processes %d and %d", ma.proc.Pid, mc.proc.Pid)
		}
		result, err := ma.ext.Tools["ask"].Definition.Execute(t.Context(), "ask", json.RawMessage(`{}`), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := result.(agent.AgentToolResult).Text(); got != `{"a":true,"c":true}` {
			t.Errorf("bus after reload=%v: %s", reload, got)
		}
		if reload {
			report := h.LastReloadReport()
			if len(report.Cells) != 2 {
				t.Fatalf("reload report counts admissions instead of processes: %+v", report.Cells)
			}
			for _, cell := range report.Cells {
				if cell.Strategy == CellStrategyPackedNode && (len(cell.Extensions) != 2 || cell.BinaryPath == "" || cell.Hash == "") {
					t.Fatalf("incomplete shared Node report: %+v", cell)
				}
			}
		}
	}
}
