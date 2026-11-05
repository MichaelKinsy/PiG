// Ports packages/durable/src/session/transaction.ts (adoption only). The module the upstream session tests import by path
// (`../src/session/transaction.ts`): `Transaction.prototype.adopt` and `discard` are what they spy on. The core owns the staging of a
// commit's changes and applies a batch at its durability receipt, so this class is the host's side of one batch of a Storage.commit call:
// `adopt` is the host's adoption of the received batch (a throw there fails the commit with it, as a throw of memory's adoption does), and
// `discard` is the rejection of a batch the Storage never received (the core discards what it staged when the call's failure reaches it).
export class Transaction {
  adopted = false
  discarded = false

  /** The Storage received the batch at `seq`. Throws when the host cannot adopt it. */
  adopt(_seq: number): void {
    this.adopted = true
  }

  /** The publication of a commit held in memory (session.ts #hold): the host's side of its adoption; a throw fails the commit with it. */
  adoptHeld(): void {}

  /** The Storage rejected the batch before adopting it: nothing it staged is kept. */
  discard(): void {
    this.discarded = true
  }
}
