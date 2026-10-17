package codemode

// qjsExports lists the QuickJS exports a vm calls, in the order of QJS_EXPORTS in
// durable/core/host/cf/src/codemode/quickjs.ts: the Worker host's QuickJS instance takes a call by index into this list.
// The Go test TestQJSExportsMatchTheWorkerHost fails when the two lists differ.
var qjsExports = [...]string{
	"qjs_init",
	"qjs_set_memory_limit",
	"qjs_set_max_stack_size",
	"qjs_set_interrupt_handler",
	"qjs_get_undefined",
	"qjs_get_false",
	"qjs_get_true",
	"qjs_free_value",
	"qjs_dup_value",
	"qjs_new_string",
	"qjs_new_number",
	"qjs_get_float64",
	"qjs_is_undefined",
	"qjs_get_bool",
	"qjs_get_string_len",
	"qjs_get_prop_string",
	"qjs_get_exception",
	"qjs_is_exception",
	"qjs_eval",
	"qjs_call",
	"qjs_new_host_function",
	"qjs_is_job_pending",
	"qjs_execute_pending_job",
	"wasm_malloc",
	"wasm_free",
	"qjs_init2",
	"qjs_get_quickjs_version",
}
