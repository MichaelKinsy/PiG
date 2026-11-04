// The source of a candidate directory's index.ts that re-exports the reference and calls every conversation view listener two microtasks
// late (the negative control of the output level): `Harness.open` returns a Harness whose conversations (`root`, `createConversation`,
// `conversation`) hand out watches whose `start` delays the listener. `index` is the reference's index module URL.
export const lateListenerSource = index => `import * as real from "${index}"
export * from "${index}"
const through = (target: any, wrap: (key: string | symbol, value: any) => any) => new Proxy(target, {
	get(t, key) {
		const value = Reflect.get(t, key, t)
		const wrapped = wrap(key, value)
		return wrapped !== undefined ? wrapped : typeof value === "function" ? value.bind(t) : value
	},
})
const late = (watch: any) => through(watch, (key, start) => key === "start" ? (listener: any) => start.call(watch, async (change: any, context: any) => { await Promise.resolve(); await Promise.resolve(); return listener(change, context) }) : undefined)
const conversation = (c: any) => through(c, (key, watch) => key === "watch" ? async (...args: any[]) => late(await watch.apply(c, args)) : undefined)
const harness = (h: any) => through(h, (key, method) => key === "root" || key === "createConversation" || key === "conversation" ? async (...args: any[]) => conversation(await method.apply(h, args)) : undefined)
export const Harness = { ...real.Harness, open: async (...args: any[]) => harness(await (real.Harness.open as any)(...args)) }
`
