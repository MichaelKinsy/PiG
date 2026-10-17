// Records upstream's parseEvalCli behavior for cli_test.go.
//
// Run with Node 24 from a directory that holds the upstream cli.ts:
//   node cli_oracle.ts <path to .upstream/v1.0.4/packages/evals/src/cli.ts> > cli_oracle.json
import { readFileSync, writeFileSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const source = readFileSync(process.argv[2], "utf8");
const directory = mkdtempSync(join(tmpdir(), "cli-oracle-"));
writeFileSync(join(directory, "parse.ts"), `${source.slice(source.indexOf("type EvalCliOptions"), source.indexOf("const packageRoot"))}\nexport { parseEvalCli };\n`);
const { parseEvalCli } = await import(join(directory, "parse.ts"));
const cases: Array<{args: string[]; env: Record<string,string>}> = [
 {args:[],env:{}},
 {args:["--provider","p","--model","m"],env:{}},
 {args:["--provider=p","--model=m","--runs-per-variant=5"],env:{}},
 {args:["--provider","p"],env:{}},
 {args:["--model","m"],env:{PI_PROVIDER:"a",PI_MODEL:"b"}},
 {args:[],env:{PI_PROVIDER:"a",PI_MODEL:"b"}},
 {args:[],env:{PI_PROVIDER:" a ",PI_MODEL:" b\t"}},
 {args:[],env:{PI_PROVIDER:"a"}},
 {args:[],env:{PI_MODEL:"b"}},
 {args:[],env:{PI_PROVIDER:"  ",PI_MODEL:"b"}},
 {args:[],env:{PI_PROVIDER:"  ",PI_MODEL:"  "}},
 {args:["--provider"],env:{}},
 {args:["--provider","--model"],env:{}},
 {args:["--provider="],env:{}},
 {args:["--provider"," ","--model","m"],env:{}},
 {args:["--runs-per-variant","3"],env:{}},
 {args:["--runs-per-variant","0"],env:{}},
 {args:["--runs-per-variant","1.5"],env:{}},
 {args:["--runs-per-variant","abc"],env:{}},
 {args:["--runs-per-variant","1e1"],env:{}},
 {args:["--runs-per-variant","0x10"],env:{}},
 {args:["--runs-per-variant","-1"],env:{}},
 {args:["--runs-per-variant=-1"],env:{}},
 {args:[],env:{PI_EVAL_RUNS_PER_VARIANT:"4"}},
 {args:[],env:{PI_EVAL_RUNS_PER_VARIANT:"  "}},
 {args:[],env:{PI_EVAL_RUNS_PER_VARIANT:"9007199254740993"}},
 {args:["--runs-per-variant","2"],env:{PI_EVAL_RUNS_PER_VARIANT:"4"}},
 {args:["evals/a.docs.eval.ts","evals/b.docs.eval.ts"],env:{}},
 {args:["-t","adds the model"],env:{}},
 {args:["-t"],env:{}},
 {args:["-t",""],env:{}},
 {args:["-t","-x"],env:{}},
 {args:["--testNamePattern","x","--testNamePattern=y"],env:{}},
 {args:["--testNamePattern="],env:{}},
 {args:["--testNamePattern"],env:{}},
 {args:["-t","a.docs.eval.ts"],env:{}},
 {args:["--bogus"],env:{}},
 {args:["--provider=p"],env:{}},
 {args:["--provider=p","--model","m","x.docs.eval.ts","-t","n"],env:{}},
 {args:["--provider","p=q","--model","m"],env:{}},
 {args:["--runs-per-variant=3=4"],env:{}},
 {args:["foo.eval.ts"],env:{}},
];
const out = cases.map(({args, env}) => { try { return {args, env, result: parseEvalCli(args, env as never)}; } catch (e) { return {args, env, error: (e as Error).message}; } });
console.log(JSON.stringify(out));
