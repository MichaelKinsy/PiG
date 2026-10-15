const pi = await import(process.argv[2]);
const out = {};
for (const n of ["Read","Bash","Edit","Write","Grep","Find","Ls","PowerShell"]) {
  const f = pi[`create${n}ToolDefinition`];
  if (!f) { out[n] = null; continue; }
  const d = f("/work");
  out[n] = { name: d.name, label: d.label, description: d.description, promptSnippet: d.promptSnippet, promptGuidelines: d.promptGuidelines, parameters: d.parameters, renderShell: d.renderShell, executionMode: d.executionMode, constrainedSampling: d.constrainedSampling };
}
console.log(JSON.stringify(out, null, 1));
