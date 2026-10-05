import { readFileSync } from "node:fs";
const root = new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/pi-ai/", import.meta.url);
if (JSON.parse(readFileSync(new URL("package.json", root), "utf8")).version !== "1.0.3") throw new Error("Expected Pi 1.0.3");
const { openaiCodexOAuth } = await import(new URL("dist/auth/oauth/openai-codex.js", root));
const access = `header.${Buffer.from('{"https://api.openai.com/auth":{"chatgpt_account_id":"account-parity"}}').toString("base64")}.signature`;
let polls = 0;
const response = (status,body) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
globalThis.fetch = async (input,init) => {
 const path = new URL(String(input)).pathname;
 if (path === "/api/accounts/deviceauth/usercode") return response(200,{device_auth_id:"device-auth-id",user_code:"ABCD-1234",interval:"0"});
 if (path === "/api/accounts/deviceauth/token") {
  polls++;
  if (polls === 1) return response(403,{error:"deviceauth_authorization_pending"});
  return response(200,{authorization_code:"oauth-code",code_verifier:"device-code-verifier"});
 }
 if (path === "/oauth/token") {
  if (new URLSearchParams(String(init.body)).get("grant_type") === "refresh_token") return response(401,{error:{message:"Could not validate your token. Please try signing in again.",type:"invalid_request_error"}});
  return response(200,{access_token:access,refresh_token:"refresh-token",expires_in:3600});
 }
 throw new Error(`unexpected URL ${input}`);
};
let selection, device;
const signal = new AbortController().signal;
const start = Date.now();
const credentials = await openaiCodexOAuth.login({signal,prompt:async prompt => {selection=`${prompt.message}|${prompt.options[0].id}|${prompt.options[1].id}`;return "device_code";},notify:event => {if(event.type === "device_code")device=event;}});
const expiry = credentials.expires >= start + 3600000 && credentials.expires <= Date.now() + 3600000;
let refreshError;
try {await openaiCodexOAuth.refresh({type:"oauth",access:"invalid-access-token",refresh:"invalid-refresh-token",expires:0},signal);} catch(error) {refreshError=error.message;}
if (!refreshError) throw new Error("refresh unexpectedly succeeded");
console.log(JSON.stringify({selection,device:`${device.userCode}|${device.verificationUri}|${device.intervalSeconds}|${device.expiresInSeconds}`,polls,account:credentials.accountId,tokens:credentials.access === access && credentials.refresh === "refresh-token",expiry,refreshError}));
