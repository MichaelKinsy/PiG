import { appendList, pendingAssistantFrames, pendingToolOutput, setValue } from "../session/values.js";
export async function readAssistantFrames(reader, operationId, responseEntryId, context) {
    const frames = [];
    let cursor;
    for (;;) {
        const page = await reader.readList(pendingAssistantFrames(operationId, responseEntryId), { order: "asc", limit: 1_000, ...(cursor === undefined ? {} : { cursor }) }, context);
        frames.push(...page.map(({ value }) => value));
        if (page.length < 1_000)
            return frames;
        cursor = { seq: page[page.length - 1].seq };
    }
}
function openProgress(lane, drive, commitWrite, stillOwns) {
    let sealed = false;
    let latest = Promise.resolve();
    return {
        write(item) {
            if (sealed)
                return;
            const write = lane
                .command((projection) => {
                if (!stillOwns(projection))
                    return { kind: "return", result: undefined };
                return {
                    kind: "commit",
                    writes: [commitWrite(item)],
                    next: projection,
                    materialize: () => undefined,
                };
            }, drive.context)
                .then(() => undefined);
            latest = write;
            void write.catch(() => { });
        },
        seal() {
            sealed = true;
        },
        async drain() {
            await latest;
        },
    };
}
export function openFrameProgress(lane, drive, responseEntryId) {
    const address = pendingAssistantFrames(drive.operationId, responseEntryId);
    return openProgress(lane, drive, (frame) => appendList(address, frame), (state) => {
        const run = state.operation?.state;
        if (run === undefined)
            return false;
        return ((run.at === "assistant.effect_pending" || run.at === "deferred.effect_pending") &&
            run.responseEntryId === responseEntryId);
    });
}
export function openToolProgress(lane, drive, turnId, sourceIndex, invocationId) {
    const address = pendingToolOutput(drive.operationId, invocationId);
    return openProgress(lane, drive, (snapshot) => setValue(address, snapshot), (state) => {
        const operation = state.operation;
        if (operation?.state.at !== "tools")
            return false;
        const batch = operation.state.batch;
        return (batch.turnId === turnId &&
            batch.calls.some((call) => call.sourceIndex === sourceIndex &&
                call.resultEntryId === invocationId &&
                call.status === "effect_pending"));
    });
}
//# sourceMappingURL=progress.js.map