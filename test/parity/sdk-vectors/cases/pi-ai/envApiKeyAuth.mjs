// envApiKeyAuth returns an auth object: the case builds it, resolves it over the stored credential and environment (recording which variables it asked for), or runs its login.
export const cases = [
  { name: "resolve 01", args: ["Name", ["FIRST", "SECOND"], undefined, {}] },
  { name: "resolve 02", args: ["Name", ["FIRST", "SECOND"], undefined, {"FIRST": "a"}] },
  { name: "resolve 03", args: ["Name", ["FIRST", "SECOND"], undefined, {"SECOND": "b"}] },
  { name: "resolve 04", args: ["Name", ["FIRST", "SECOND"], undefined, {"FIRST": "a", "SECOND": "b"}] },
  { name: "resolve 05", args: ["Name", ["FIRST", "SECOND"], undefined, {"FIRST": "", "SECOND": "b"}] },
  { name: "resolve 06", args: ["Name", ["FIRST", "SECOND"], undefined, {"FIRST": ""}] },
  { name: "resolve 07", args: ["Name", ["FIRST", "SECOND"], {"type": "api_key", "key": "stored"}, {"FIRST": "a"}] },
  { name: "resolve 08", args: ["Name", ["FIRST", "SECOND"], {"type": "api_key", "key": "stored", "env": {"X": "1"}}, {}] },
  { name: "resolve 09", args: ["Name", ["FIRST", "SECOND"], {"type": "api_key", "key": ""}, {"FIRST": "a"}] },
  { name: "resolve 10", args: ["Name", ["FIRST", "SECOND"], {"type": "api_key"}, {"SECOND": "b"}] },
  { name: "resolve 11", args: ["Name", ["FIRST", "SECOND"], {"type": "api_key", "key": "stored", "env": {}}, {"FIRST": "a"}] },
  { name: "resolve 12", args: ["None", [], null, {}] },
  { name: "login", args: ["Name", ["FIRST", "SECOND"], "$login", {}] },
];
export const call = async (fn, name, envVars, credential, env) => {
  const auth = fn(name, envVars);
  const signal = new AbortController().signal;
  if (credential === "$login") {
    const prompts = [];
    const result = await auth.login({ signal, prompt: async (request) => { prompts.push(request); return "typed-key"; } });
    return json({ prompts, result, hasCheck: auth.check !== undefined });
  }
  const seen = [];
  const result = await auth.resolve({ ctx: { env: async (variable) => { seen.push(variable); return env[variable]; } }, credential, signal });
  return json({ result: result ?? null, seen });
};

// A property Pi leaves undefined is no member of the answer (JSON.stringify drops it), so the vector records the JSON the answer has.
const json = (value) => (value === undefined ? undefined : JSON.parse(JSON.stringify(value)));
