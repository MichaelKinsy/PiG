// pi-durable's three public error classes (src/errors.ts), reconstructed from the {name, message} pair a rejected step or an
// api answer carries. Other names revive as a plain Error with that name.

/** A transaction read a table after its first table write. */
export class ReadAfterWrite extends Error {
  constructor(method: string) {
    super(`Tx.${method}() cannot read tables after the first table write`)
    this.name = "ReadAfterWrite"
  }
}

/** Storage rejected a batch before any durable effect; the owning Session may continue safely. */
export class StorageRejected extends Error {
  constructor(message: string, options?: ErrorOptions) {
    super(message, options)
    this.name = "StorageRejected"
  }
}

/** A submission reached a busy conversation and was not admitted. */
export class ConversationBusy extends Error {
  readonly conversationId: number | string
  constructor(conversationId: number | string) {
    super(`Conversation ${conversationId} is busy`)
    this.name = "ConversationBusy"
    this.conversationId = conversationId
  }
}

export type WireErrorBody = { name?: string; message?: string; conversationId?: number | string; stack?: string }

/** Rebuild the error Pi would have thrown from its wire form. */
export function reviveError(body: WireErrorBody): Error {
  const message = body.message ?? ""
  let error: Error
  switch (body.name) {
    case "ConversationBusy": {
      error = new ConversationBusy(body.conversationId ?? /\d+/.exec(message)?.[0] ?? "")
      error.message = message || error.message
      break
    }
    case "ReadAfterWrite": error = Object.assign(new ReadAfterWrite("doc"), { message }); break
    case "StorageRejected": error = new StorageRejected(message); break
    default: {
      error = new Error(message)
      error.name = body.name ?? "Error"
    }
  }
  if (body.stack) error.stack = body.stack
  return error
}
