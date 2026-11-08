package envs

import (
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/envs/sandbox-schema.ts: what the sandbox launcher on a host prints (p2-sandbox.sh check,
// p2-sandbox.mjs status/start/stop): wire input that the manager checks before use.

func compileSchema(value schema.Schema) *schema.Validator {
	compiled, err := schema.Compile(value, schema.CompileOptions{})
	if err != nil {
		panic(err)
	}
	return compiled
}

// LauncherCheckSchema is `p2-sandbox.sh check`: how Gondolin can run on the host and what it found.
var LauncherCheckSchema = schema.Obj(schema.Properties{
	schema.Prop("runtime", schema.Union([]schema.Schema{p2runtime.Literal("native"), p2runtime.Literal("none")})),
	schema.Prop("system", schema.Str()),
	schema.Prop("arch", schema.Str()),
	schema.Prop("kvm", schema.Bool()),
	schema.Prop("kvmDevice", schema.Bool()),
	schema.Prop("qemu", schema.Str()),
	schema.Prop("node", schema.Str()),
}, schema.Open())

// LauncherStatusSchema is `status`, `start` and `stop`: the VM's state; a running one names its configuration's revision.
var LauncherStatusSchema = schema.Union([]schema.Schema{
	schema.Obj(schema.Properties{schema.Prop("state", p2runtime.Literal("stopped"))}, schema.Open()),
	schema.Obj(schema.Properties{
		schema.Prop("state", p2runtime.Literal("running")), schema.Prop("startedAt", schema.Num()), schema.Prop("revision", schema.Str()),
		schema.Prop("cpus", schema.Num()), schema.Prop("memoryMb", schema.Num()),
	}, schema.Open()),
})

var (
	checkReport  = compileSchema(LauncherCheckSchema)
	statusReport = compileSchema(LauncherStatusSchema)
)
