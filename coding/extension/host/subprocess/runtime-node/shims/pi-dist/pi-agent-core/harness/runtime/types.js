import { withoutAbortSignal } from "../context.js";
import { createGate } from "../execution/effect-gate.js";
export class SliceNotImplemented extends Error {
    constructor(operation) {
        super(`${operation} is not implemented until its later AgentHarness slice`);
        this.name = "SliceNotImplemented";
    }
}
/** One installed process-local drive pass. */
export class Drive {
    operationId;
    completion;
    gate;
    context;
    waitForRetry;
    closeSignal;
    deferredPermits;
    control;
    closeController;
    resolveCompletion;
    rejectCompletion;
    constructor(options, context) {
        this.operationId = options.operationId;
        this.context = withoutAbortSignal(context);
        this.waitForRetry = options.waitForRetry ?? false;
        this.deferredPermits = options.pollDeferred === true ? 1 : 0;
        let resolveCompletion;
        let rejectCompletion;
        this.completion = new Promise((resolve, reject) => {
            resolveCompletion = resolve;
            rejectCompletion = reject;
        });
        this.resolveCompletion = resolveCompletion;
        this.rejectCompletion = rejectCompletion;
        void this.completion.catch(() => { });
        const { gate, control } = createGate();
        this.gate = gate;
        this.control = control;
        this.closeController = new AbortController();
        this.closeSignal = this.closeController.signal;
    }
    settle(outcome) {
        this.resolveCompletion(outcome);
    }
    fail(error) {
        this.rejectCompletion(error);
    }
    beginAbort(cancellation) {
        this.control.beginAbort(cancellation);
    }
    signalAbort() {
        this.control.signalAbort();
    }
    closeGate(error) {
        this.control.close(error);
        if (!this.closeSignal.aborted)
            this.closeController.abort(error);
        this.rejectCompletion(error);
    }
}
//# sourceMappingURL=types.js.map