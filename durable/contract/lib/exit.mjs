/**
 * End the process with `code` once what console.log and console.error queued has been written. process.exit() right after them
 * drops what a pipe has not yet taken (a pipe takes one page when its user is over the kernel's pipe limit), which cuts a report
 * the gate's caller reads. Await it: it never returns.
 */
export async function exitAfterFlush(code) {
	await new Promise(done => process.stderr.write("", done))
	await new Promise(done => process.stdout.write("", done))
	process.exit(code)
}
