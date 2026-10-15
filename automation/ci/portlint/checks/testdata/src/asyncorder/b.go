// Ports packages/ai/src/api/not-in-ledger.ts
package asyncorder

func unlisted(f func()) {
	go f()
}
