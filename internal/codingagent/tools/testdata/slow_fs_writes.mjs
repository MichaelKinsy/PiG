// Test shim for the Pi oracle (node --import): every fs.write and fs.writev lands in two parts with a pause between them, so a file Pi is writing is visibly incomplete for a while, as it is on a loaded disk.
import fs from "node:fs";

const pauseMs = 150;
const write = fs.write;

// writeAll writes buffer[offset, offset+length) at position and calls done(error, written) once every byte is out: fs.write may write fewer bytes than asked, so each call continues after the bytes it actually wrote.
function writeAll(fd, buffer, offset, length, position, done) {
  let written = 0;
  const next = () => {
    if (written === length) return done(null, written);
    write.call(fs, fd, buffer, offset + written, length - written, typeof position === "number" ? position + written : null, (error, count) => {
      if (error) return done(error, written);
      written += count;
      next();
    });
  };
  next();
}

fs.write = function (fd, buffer, offset, length, position, callback) {
  if (typeof callback !== "function" || length < 2) return write.apply(fs, arguments);
  const half = length >> 1;
  writeAll(fd, buffer, offset, half, position, (error, first) => {
    if (error) return callback(error, first, buffer);
    setTimeout(() => {
      const next = typeof position === "number" ? position + half : null;
      writeAll(fd, buffer, offset + half, length - half, next, (error, second) => callback(error, first + second, buffer));
    }, pauseMs);
  });
};

fs.writev = function (fd, buffers, position, callback) {
  const joined = Buffer.concat(buffers);
  fs.write(fd, joined, 0, joined.length, typeof position === "number" ? position : null, (error, written) => callback(error, written, buffers));
};
