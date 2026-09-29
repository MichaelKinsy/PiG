import { decoder, encoder } from "../delta/index.js";
class StateCodecRegistry {
    #create;
    #entries = new Map();
    constructor(create) {
        this.#create = create;
    }
    reset() {
        this.#entries.clear();
    }
    add(instance, member) {
        const key = stateKey(instance, member);
        if (this.#entries.has(key))
            throw new Error(`Duplicate service state ${describeState(instance, member)}`);
        const codec = this.#create();
        this.#entries.set(key, { instance, codec });
        return codec;
    }
    get(instance, member) {
        const codec = this.#entries.get(stateKey(instance, member))?.codec;
        if (codec === undefined)
            throw new Error(`Unknown service state ${describeState(instance, member)}`);
        return codec;
    }
    removeInstance(instance) {
        for (const [key, entry] of this.#entries) {
            if (sameAddress(entry.instance, instance))
                this.#entries.delete(key);
        }
    }
}
export function createServiceStateEncoder() {
    const codecs = new StateCodecRegistry(encoder);
    return {
        encodeSnapshot(snapshot) {
            codecs.reset();
            return {
                ...snapshot,
                instances: snapshot.instances.map((instance) => encodeInstance(instance, codecs)),
            };
        },
        encodeUpdate(update) {
            switch (update.type) {
                case "state":
                    return { ...update, ops: codecs.get(update.instance, update.member).encode(update.ops) };
                case "replaced":
                    codecs.reset();
                    return { ...update, snapshot: encodeInstance(update.snapshot, codecs) };
                case "spawned":
                    return { ...update, instance: encodeInstance(update.instance, codecs) };
                case "unavailable":
                    codecs.reset();
                    return update;
                case "closed":
                    codecs.removeInstance(update.instance);
                    return update;
            }
        },
    };
}
export function createServiceStateDecoder() {
    const codecs = new StateCodecRegistry(decoder);
    return {
        decodeSnapshot(snapshot) {
            codecs.reset();
            return {
                ...snapshot,
                instances: snapshot.instances.map((instance) => decodeInstance(instance, codecs)),
            };
        },
        decodeUpdate(update) {
            switch (update.type) {
                case "state":
                    return { ...update, ops: codecs.get(update.instance, update.member).decode(update.ops) };
                case "replaced":
                    codecs.reset();
                    return { ...update, snapshot: decodeInstance(update.snapshot, codecs) };
                case "spawned":
                    return { ...update, instance: decodeInstance(update.instance, codecs) };
                case "unavailable":
                    codecs.reset();
                    return update;
                case "closed":
                    codecs.removeInstance(update.instance);
                    return update;
            }
        },
    };
}
function encodeInstance(instance, codecs) {
    return {
        ...instance,
        members: instance.members.map((member) => member.kind === "state"
            ? { ...member, ops: codecs.add(instance.instance, member.name).encode(member.ops) }
            : member),
    };
}
function decodeInstance(instance, codecs) {
    return {
        ...instance,
        members: instance.members.map((member) => member.kind === "state"
            ? { ...member, ops: codecs.add(instance.instance, member.name).decode(member.ops) }
            : member),
    };
}
function stateKey(instance, member) {
    return JSON.stringify([instance?.key ?? null, instance?.generation ?? null, member]);
}
function sameAddress(left, right) {
    return left?.key === right.key && left.generation === right.generation;
}
function describeState(instance, member) {
    return instance === undefined ? member : `${instance.key}@${instance.generation}.${member}`;
}
//# sourceMappingURL=state-codec.js.map