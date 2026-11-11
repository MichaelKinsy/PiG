// pi-durable's three public error classes (src/errors.ts), reconstructed from the {name, message} pair a rejected step or an
// api answer carries. Other names revive as a plain Error with that name.

/** A transaction read a table after its first table write, or admitted a submission (`submit(tx, draft)`) after it. */
export class ReadAfterWrite extends Error {
  /** `operation` names the call, such as `Tx.task()`. */
  constructor(operation: string) {
    super(`${operation} cannot read tables after the first table write`)
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

/**
 * The Harness was opened read-only (`HarnessOptions.readOnly`): a commit with writes, or a call that would run tasks, was refused. Nothing
 * was written, and the Harness stays usable for reads.
 */
export class ReadOnlySession extends Error {
  constructor(what: string) {
    super(`Read-only Harness: ${what} refused`)
    this.name = "ReadOnlySession"
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

/** An input with `whenIdle: "reject"` reached an idle conversation and was not admitted. */
export class ConversationIdle extends Error {
  readonly conversationId: number
  constructor(conversationId: number | string) {
    super(`Conversation ${conversationId} is idle`)
    this.name = "ConversationIdle"
    this.conversationId = Number(conversationId)
  }
}

export type WireErrorBody = { name?: string; message?: string; conversationId?: number | string; stack?: string; cause?: unknown; errors?: WireErrorBody[] }

/** The message of a thrown value; a value that cannot be formatted (a null-prototype object) must not fail the code that formats it (errors.ts errorMessage). */
export function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message
  try { return String(error) } catch { return Object.prototype.toString.call(error) }
}

/** The Storage failure that failed the Session: the value its Storage threw, which is not always an Error (`undefined`, `null`). */
export type StorageFailure = { error: unknown }

/** The {name, message} the core carries for a thrown value. */
export const wireError = (error: unknown): { name: string; message: string } => error instanceof Error ? { name: error.name, message: error.message } : { name: "Error", message: errorMessage(error) }

/**
 * Rebuild the error Pi would have thrown from its wire form. `failure` is the Storage error that failed the Session, which the host
 * still holds as the value its Storage threw: the call that hit it rejects with that value itself, and `SessionFailed` carries it
 * as its cause.
 */
export function reviveError(body: WireErrorBody, failure?: StorageFailure): any {
  const message = body.message ?? ""
  let error: Error
  if (failure !== undefined && body.name === "SessionFailed") return new SessionFailed(failure.error)
  if (failure !== undefined) {
    const wire = wireError(failure.error)
    if ((body.name ?? "Error") === wire.name && message === wire.message) return failure.error
  }
  switch (body.name) {
    case "ConversationBusy": {
      error = new ConversationBusy(body.conversationId ?? /\d+/.exec(message)?.[0] ?? "")
      error.message = message || error.message
      break
    }
    case "ConversationIdle": {
      error = new ConversationIdle(body.conversationId ?? /\d+/.exec(message)?.[0] ?? "")
      error.message = message || error.message
      break
    }
    case "ReadOnlySession": error = Object.assign(new ReadOnlySession("a commit with writes"), { message }); break
    case "ReadAfterWrite": error = Object.assign(new ReadAfterWrite("Tx.doc()"), { message }); break
    case "StorageRequestError": error = new StorageRequestError(message); break
    case "RangeError": error = new RangeError(message); break
    // An afterRun hook's rejected requests (generation.ts prepareRunEndTasks): the notice carries each one's {name, message}.
    case "AggregateError": error = new AggregateError((body.errors ?? []).map(each => reviveError(each)), message); break
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
