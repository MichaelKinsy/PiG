// Preloaded into the Pi oracle (NODE_OPTIONS=--require) to force the schedule that Windows CI produced once: libuv there writes the buffers of one fs.writev one WriteFile at a time, and a write thread that is descheduled between two of them leaves a prefix of Pi's spill file standing. Here the first two buffers of a writev land at once and the rest only after a pause far longer than the oracle ever waited between two looks at the file.
const fs = require("node:fs");
const writev = fs.writev;
const PAUSE_MS = 300;
fs.writev = function (fd, buffers, ...rest) {
  const callback = rest.pop();
  if (buffers.length < 3) return writev.call(this, fd, buffers, ...rest, callback);
  // Pi's stream appends: its writes carry no position.
  const head = fs.writevSync(fd, buffers.slice(0, 2));
  setTimeout(() => {
    writev.call(fs, fd, buffers.slice(2), (error, written) => callback(error, error ? written : head + written, buffers));
  }, PAUSE_MS);
};
