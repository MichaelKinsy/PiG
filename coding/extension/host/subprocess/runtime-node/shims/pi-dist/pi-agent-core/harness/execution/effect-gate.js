/** Expected internal control flow when cancellation wins effect admission. */
export class AbortRequested extends Error {
    cancellation;
    constructor(cancellation) {
        super("Abort requested");
        this.name = "AbortRequested";
        this.cancellation = cancellation;
    }
}
/** Create separate procedure-facing and owner-facing views of one effect gate. */
export function createGate() {
    let state = { status: "open" };
    const controller = new AbortController();
    const check = () => {
        if (state.status === "aborting")
            throw new AbortRequested(state.cancellation);
        if (state.status === "closed")
            throw state.error;
    };
    return {
        gate: {
            admit(invoke) {
                check();
                return invoke();
            },
            signal: controller.signal,
        },
        control: {
            beginAbort(cancellation) {
                if (state.status !== "open")
                    return;
                state = { status: "aborting", cancellation };
            },
            signalAbort() {
                if (state.status !== "aborting" || controller.signal.aborted)
                    return;
                controller.abort(new AbortRequested(state.cancellation));
            },
            close(error) {
                if (state.status === "closed")
                    return;
                state = { status: "closed", error };
                if (!controller.signal.aborted)
                    controller.abort(error);
            },
        },
    };
}
//# sourceMappingURL=effect-gate.js.map