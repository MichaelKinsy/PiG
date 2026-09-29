// pig additive (D19): the subprocess wire assembles one bounded length-prefixed frame without repeatedly copying an incomplete body.
export class FrameBuffer {
  constructor(maxFrameSize, onFrame) {
    this.maxFrameSize = maxFrameSize;
    this.onFrame = onFrame;
    this.header = Buffer.allocUnsafe(4);
    this.headerOffset = 0;
    this.body = undefined;
    this.bodyOffset = 0;
  }

  write(chunk) {
    let offset = 0;
    while (offset < chunk.length) {
      if (this.body === undefined) {
        const count = Math.min(4 - this.headerOffset, chunk.length - offset);
        chunk.copy(this.header, this.headerOffset, offset, offset + count);
        offset += count;
        this.headerOffset += count;
        if (this.headerOffset < 4) return;
        const size = this.header.readUInt32BE(0);
        if (size > this.maxFrameSize) throw new Error(`frame too large: ${size}`);
        this.headerOffset = 0;
        // No body byte is exposed until every byte in this allocation has been filled.
        this.body = Buffer.allocUnsafe(size);
        this.bodyOffset = 0;
      }
      const count = Math.min(this.body.length - this.bodyOffset, chunk.length - offset);
      chunk.copy(this.body, this.bodyOffset, offset, offset + count);
      offset += count;
      this.bodyOffset += count;
      if (this.bodyOffset < this.body.length) return;
      const body = this.body;
      this.body = undefined;
      this.onFrame(body);
    }
  }
}
