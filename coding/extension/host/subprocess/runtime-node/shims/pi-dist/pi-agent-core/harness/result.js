export const Result = {
    ok(value) {
        return { ok: true, value };
    },
    err(error) {
        return { ok: false, error };
    },
    isOk(result) {
        return result.ok;
    },
    isErr(result) {
        return !result.ok;
    },
};
export function TaggedError(tag) {
    class TaggedErrorClass extends Error {
        _tag = tag;
        constructor(props) {
            super(props.message);
            this.name = tag;
            Object.assign(this, props);
        }
        toJSON() {
            const payload = {};
            for (const key of Object.keys(this)) {
                if (key !== "_tag")
                    payload[key] = this[key];
            }
            return { _tag: tag, message: this.message, ...payload };
        }
        static is(value) {
            return value instanceof TaggedErrorClass;
        }
    }
    return TaggedErrorClass;
}
export class LaneBusy extends TaggedError("LaneBusy") {
}
export class OperationMismatch extends TaggedError("OperationMismatch") {
}
export class NoActiveRun extends TaggedError("NoActiveRun") {
}
export class NoActiveOperation extends TaggedError("NoActiveOperation") {
}
export class NothingToResume extends TaggedError("NothingToResume") {
}
export class NothingToCompact extends TaggedError("NothingToCompact") {
}
export class InvalidMessage extends TaggedError("InvalidMessage") {
}
export class InvalidNavigation extends TaggedError("InvalidNavigation") {
}
export class UnknownSkill extends TaggedError("UnknownSkill") {
}
export class UnknownTemplate extends TaggedError("UnknownTemplate") {
}
export class UnknownTarget extends TaggedError("UnknownTarget") {
}
export class InvalidLane extends TaggedError("InvalidLane") {
}
export class Closed extends TaggedError("Closed") {
}
export class HarnessFault extends Error {
    cause;
    constructor(message, cause) {
        super(message);
        this.name = "HarnessFault";
        this.cause = cause;
    }
}
export class HarnessClosed extends Error {
    constructor() {
        super("AgentHarness was closed while the operation was active");
        this.name = "HarnessClosed";
    }
}
export function matchError(error, matchers) {
    const matcher = matchers[error._tag];
    return matcher(error);
}
//# sourceMappingURL=result.js.map