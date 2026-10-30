// Ports packages/durable/src/session/transaction.ts (adoption only). The module the upstream session tests import by path
// (`../src/session/transaction.ts`): `Transaction.prototype.adopt` and `discard` are what they spy on. The core owns the staging of a
// commit's changes, so this class is the host's side of one Storage.commit call: `adopt` tells the core the Storage made the batch
// durable (the core applies and publishes the commit inside that call), and `discard` is the rejection of a batch the Storage never
// adopted (the core discards what it staged when the call's rejection reaches it).
export class Transaction {
  #send: (seq: number) => void
  adopted = false
  discarded = false

  constructor(send: (seq: number) => void) { this.#send = send }

  /** The Storage adopts the batch at `seq`. Throws when the core cannot take the adoption. */
  adopt(seq: number): void {
    this.#send(seq)
    this.adopted = true
  }

  /** The Storage rejected the batch before adopting it: nothing it staged is kept. */
  discard(): void {
    this.discarded = true
  }
}
