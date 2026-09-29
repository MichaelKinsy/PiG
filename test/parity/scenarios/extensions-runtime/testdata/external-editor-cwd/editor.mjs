// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT
import { readFileSync, readSync, writeFileSync } from "node:fs";
const path = process.argv.at(-1);
process.stdout.write("external-child-ready\n");
const input = Buffer.alloc(256);
const n = readSync(0, input);
writeFileSync(path, `${readFileSync(path, "utf8")}\n${input.subarray(0, n).toString().trim()}\n`);
