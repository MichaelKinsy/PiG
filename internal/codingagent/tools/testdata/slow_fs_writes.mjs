// Test shim for the Pi oracle (node --import): every fs.write and fs.writev lands in two parts with a pause between them, so a file Pi is writing is visibly incomplete for a while, as it is on a loaded disk.
import fs from "node:fs";

const pauseMs = 150;
const write = fs.write;

fs.write = function (fd, buffer, offset, length, position, callback) {
  if (typeof callback !== "function" || length < 2) return write.apply(fs, arguments);
  const half = length >> 1;
  write.call(fs, fd, buffer, offset, half, position, (error, first) => {
    if (error) return callback(error, first, buffer);
    setTimeout(() => {
      const next = typeof position === "number" ? position + half : null;
      write.call(fs, fd, buffer, offset + half, length - half, next, (error, second) => callback(error, first + (second ?? 0), buffer));
    }, pauseMs);
  });
};

fs.writev = function (fd, buffers, position, callback) {
  const joined = Buffer.concat(buffers);
  fs.write(fd, joined, 0, joined.length, typeof position === "number" ? position : null, (error, written) => callback(error, written, buffers));
};
