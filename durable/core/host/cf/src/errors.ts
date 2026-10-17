// pi-durable's three public error classes (src/errors.ts), reconstructed from the {name, message} pair a rejected step or an
// api answer carries. Other names revive as a plain Error with that name.

/** A transaction read a table after its first table write. */
export class ReadAfterWrite extends Error {
  constructor(method: string) {
    super(`Tx.${method}() cannot read tables after the first table write`)
    this.name = "ReadAfterWrite"
  }
}

/** A Storage read the caller's request got wrong (a cursor or query the Storage rejects): the Session continues. */
export class StorageRequestError extends Error {
  constructor(message: string) {
    super(message)
    this.name = "StorageRequestError"
  }
}

/**
 * The Session failed, so nothing runs or commits any more. It fails when a Storage method throws, a commit cannot be adopted, or the
 * Harness's own bookkeeping throws. `cause` is that first error.
 */
export class SessionFailed extends Error {
  constructor(cause: unknown) {
    super("Session failed after a storage error; close and reopen it", { cause })
    this.name = "SessionFailed"
  }
}

/** A submission reached a busy conversation and was not admitted. */
export class ConversationBusy extends Error {
  readonly conversationId: number
  constructor(conversationId: number | string) {
    super(`Conversation ${conversationId} is busy`)
    this.name = "ConversationBusy"
    this.conversationId = Number(conversationId)
  }
}

export type WireErrorBody = { name?: string; message?: string; conversationId?: number | string; stack?: string; cause?: unknown }

/** The message of a thrown value; a value that cannot be formatted (a null-prototype object) must not fail the code that formats it (errors.ts errorMessage). */
export function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  try { return String(error) } catch { return Object.prototype.toString.call(error) }
}

/**
 * Rebuild the error Pi would have thrown from its wire form. `failure` is the Storage error that failed the Session, which the host
 * still holds as the object its Storage threw: the call that hit it rejects with that error itself, and `SessionFailed` carries it
 * as its cause.
 */
export function reviveError(body: WireErrorBody, failure?: Error): Error {
  const message = body.message ?? ""
  let error: Error
  if (failure !== undefined && body.name === "SessionFailed") return new SessionFailed(failure)
  if (failure !== undefined && (body.name ?? "Error") === failure.name && message === failure.message) return failure
  switch (body.name) {
    case "ConversationBusy": {
      error = new ConversationBusy(body.conversationId ?? /\d+/.exec(message)?.[0] ?? "")
      error.message = message || error.message
      break
    }
    case "ReadAfterWrite": error = Object.assign(new ReadAfterWrite("doc"), { message }); break
    case "StorageRequestError": error = new StorageRequestError(message); break
    default: {
      error = new Error(message)
      // An Error keeps its prototype's name; only another name becomes an own property.
      if (body.name !== undefined && body.name !== "Error") error.name = body.name
    }
  }
  if (body.cause !== undefined) error.cause = body.cause
  if (body.stack) error.stack = body.stack
  return error
}
