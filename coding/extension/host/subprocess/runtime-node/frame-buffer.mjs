// pig additive (D19): the subprocess wire assembles one bounded length-prefixed frame without repeatedly copying an incomplete body.
// A body of sharedMin bytes or more is backed by its own SharedArrayBuffer, so it can reach another thread without a copy. repeatOf(size) may name an earlier body of that size: the incoming bytes are compared with it as they arrive, and a frame that repeats it is delivered as that body, with no allocation or copy.
export class FrameBuffer {
  constructor(maxFrameSize, onFrame, { sharedMin = Infinity, repeatOf } = {}) {
    this.maxFrameSize = maxFrameSize;
    this.onFrame = onFrame;
    this.sharedMin = sharedMin;
    this.repeatOf = repeatOf;
    this.header = Buffer.allocUnsafe(4);
    this.headerOffset = 0;
    // The current frame's body size, undefined while a header is incomplete.
    this.size = undefined;
    this.body = undefined;
    // The earlier body every byte of the current frame has matched so far.
    this.candidate = undefined;
    this.bodyOffset = 0;
  }

  write(chunk) {
    let offset = 0;
    while (offset < chunk.length) {
      if (this.size === undefined) {
        const count = Math.min(4 - this.headerOffset, chunk.length - offset);
        chunk.copy(this.header, this.headerOffset, offset, offset + count);
        offset += count;
        this.headerOffset += count;
        if (this.headerOffset < 4) return;
        const size = this.header.readUInt32BE(0);
        if (size > this.maxFrameSize) throw new Error(`frame too large: ${size}`);
        this.headerOffset = 0;
        this.size = size;
        this.bodyOffset = 0;
        this.candidate = size >= this.sharedMin ? this.repeatOf?.(size) : undefined;
        // No body byte is exposed until every byte in this allocation has been filled.
        if (this.candidate === undefined) this.body = this.allocate(size);
      }
      const count = Math.min(this.size - this.bodyOffset, chunk.length - offset);
      if (this.candidate !== undefined && chunk.compare(this.candidate, this.bodyOffset, this.bodyOffset + count, offset, offset + count) !== 0) {
        // The frame differs from the earlier body: keep the prefix it matched and read the rest.
        this.body = this.allocate(this.size);
        this.candidate.copy(this.body, 0, 0, this.bodyOffset);
        this.candidate = undefined;
      }
      if (this.candidate === undefined) chunk.copy(this.body, this.bodyOffset, offset, offset + count);
      offset += count;
      this.bodyOffset += count;
      if (this.bodyOffset < this.size) return;
      const repeated = this.candidate !== undefined;
      const body = repeated ? this.candidate : this.body;
      this.size = undefined;
      this.body = undefined;
      this.candidate = undefined;
      this.onFrame(body, repeated);
    }
  }

  allocate(size) {
    return size >= this.sharedMin ? Buffer.from(new SharedArrayBuffer(size)) : Buffer.allocUnsafe(size);
  }
}
