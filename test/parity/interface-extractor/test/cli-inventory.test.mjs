import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { extractCLIInventory } from "../src/cli-inventory.mjs";

function write(file, content) {
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, content);
}

function fixture() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "pig-cli-inventory-"));
  const args = path.join(root, "packages/coding-agent/src/cli/args.ts");
  const packages = path.join(root, "packages/coding-agent/src/package-manager-cli.ts");
  write(args, `
export function parseArgs(args: string[]) {
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (arg === "--help" || arg === "-h") {}
    else if (arg === "--approve" || arg === "-a") {}
    else if (arg === "--model") {}
  }
}
export function printHelp() {
  console.log(\`
  --help, -h     help
  --approve, -a  approve
  --model <id>   model
  \`);
}
`);
  write(packages, `
function printConfigCommandHelp() {
  console.log(\`
  -l, --local       local
  -a, --approve     approve
  -na, --no-approve deny
  \`);
}
function printPackageCommandHelp(command: string) {
  switch (command) {
    case "install": console.log(\`
  -l, --local       local
  -a, --approve     approve
  -na, --no-approve deny
  \`); return;
    case "remove": console.log(\`
  -l, --local       local
  -a, --approve     approve
  -na, --no-approve deny
  \`); return;
    case "update": console.log(\`
  --self             self
  -a, --approve      approve
  -na, --no-approve  deny
  \`); return;
    case "list": console.log(\`
  -a, --approve      approve
  -na, --no-approve  deny
  \`); return;
  }
}
function parsePackageCommand(args: string[]) {
  let command = args[0];
  for (const arg of args.slice(1)) {
    if (arg === "--help" || arg === "-h") {}
    else if (arg === "--local" || arg === "-l") { if (command === "install" || command === "remove") {} }
    else if (arg === "--approve" || arg === "-a") {}
    else if (arg === "--no-approve" || arg === "-na") {}
    else if (arg === "--self") { if (command === "update") {} }
  }
}
export async function handleConfigCommand(args: string[]) {
  for (const arg of args.slice(1)) {
    if (arg === "--local" || arg === "-l") {}
    else if (arg === "--approve" || arg === "-a") {}
    else if (arg === "--no-approve" || arg === "-na") {}
  }
}
`);
  return { root, args, packages };
}

test("extracts independently parsed and documented flags with command-scoped aliases", () => {
  const f = fixture();
  const inventory = extractCLIInventory({ sourceRoot: f.root, upstreamVersion: "0.83.0" });
  const byID = new Map(inventory.interfaces.map((entry) => [entry.id, entry]));
  assert.equal(inventory.interfaces.length, 21);
  assert.deepEqual(byID.get("cli:pi/--approve").aliases, ["-a"]);
  assert.equal(byID.get("cli:pi/--model").value, "<id>");
  assert.ok(byID.has("cli:pi-install/--local"));
  assert.ok(byID.has("cli:pi-remove/--local"));
  assert.ok(!byID.has("cli:pi-update/--local"));
  assert.ok(byID.has("cli:pi-update/--self"));
  assert.ok(byID.has("cli:pi-config/--no-approve"));
});

test("rejects parsed flags missing from help", () => {
  const f = fixture();
  fs.writeFileSync(f.args, fs.readFileSync(f.args, "utf8").replace("  --model <id>   model\n", ""));
  assert.throws(() => extractCLIInventory({ sourceRoot: f.root, upstreamVersion: "0.83.0" }), /cli:pi\/--model is parsed but absent/);
});

test("rejects documented flags missing from parser", () => {
  const f = fixture();
  fs.writeFileSync(f.args, fs.readFileSync(f.args, "utf8").replace('    else if (arg === "--model") {}\n', ""));
  assert.throws(() => extractCLIInventory({ sourceRoot: f.root, upstreamVersion: "0.83.0" }), /cli:pi\/--model is documented but has no parser branch/);
});

test("rejects duplicate aliases in one command scope", () => {
  const f = fixture();
  let source = fs.readFileSync(f.args, "utf8");
  source = source.replace('    else if (arg === "--model") {}', '    else if (arg === "--model") {}\n    else if (arg === "--other" || arg === "-a") {}');
  source = source.replace("  --model <id>   model", "  --model <id>   model\n  --other, -a    other");
  fs.writeFileSync(f.args, source);
  assert.throws(() => extractCLIInventory({ sourceRoot: f.root, upstreamVersion: "0.83.0" }), /alias -a belongs to both/);
});
test("recognizes reviewed internal package-manager flags without exposing them as user CLI", () => {
  const f = fixture();
  fs.appendFileSync(f.packages, `
async function runManagedNpmCi() {
  const args = ["ci", "--ignore-scripts", "--no-fund", "--no-audit"];
  return args;
}
function verifyManagedRelease() {
  return ["--version"];
}
`);
  const inventory = extractCLIInventory({ sourceRoot: f.root, upstreamVersion: "0.83.0" });
  assert.ok(!inventory.interfaces.some((entry) => entry.flag === "--ignore-scripts"));

  fs.writeFileSync(f.packages, fs.readFileSync(f.packages, "utf8").replace("--ignore-scripts", "--unreviewed-internal-flag"));
  assert.throws(() => extractCLIInventory({ sourceRoot: f.root, upstreamVersion: "0.83.0" }), /unclassified CLI flag candidate --unreviewed-internal-flag/);
});

test("rejects unclassified flag candidates", () => {
  const f = fixture();
  fs.appendFileSync(f.args, '\nconst hidden = "--hidden-interface";\n');
  assert.throws(() => extractCLIInventory({ sourceRoot: f.root, upstreamVersion: "0.83.0" }), /unclassified CLI flag candidate --hidden-interface/);
});
