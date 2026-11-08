// SPDX-License-Identifier: MIT
const fs = require("fs");
const path = require("path");
const dataFile = ["legacy", "txt"].join(".");

module.exports = {
  read() {
    return fs.readFileSync(path.join(__dirname, "data", dataFile), "utf8");
  },
  file() {
    return path.basename(__filename);
  },
};
