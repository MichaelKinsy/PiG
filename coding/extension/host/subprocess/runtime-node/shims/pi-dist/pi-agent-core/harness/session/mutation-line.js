/** Serializes complete read-modify-write jobs for one Session. */
export class MutationLine {
    tail = Promise.resolve();
    sealedError;
    run(operation) {
        if (this.sealedError !== undefined)
            return Promise.reject(this.sealedError);
        const result = this.tail.then(() => {
            if (this.sealedError !== undefined)
                throw this.sealedError;
            return operation();
        });
        this.tail = result.then(() => undefined, () => undefined);
        return result;
    }
    seal(error) {
        this.sealedError ??= error;
        return this.tail;
    }
}
//# sourceMappingURL=mutation-line.js.map