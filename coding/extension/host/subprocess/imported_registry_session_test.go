package subprocess_test

import "testing"

// Pi exposes real imported classes, independently of the main ctx objects.
// session-manager.ts:1801 and model-registry.ts:34 construct local instances.
func TestImportedExtensionRuntimeMatchesPi(t *testing.T) {
	runPinnedComparison(t, []string{"dist", "core", "extensions", "loader.js"}, "pi-coding-agent.mjs", `
const [pi,pig]=await Promise.all([import(process.argv[1]),import(process.argv[2])]);
const probe=async mod=>{
 const r=mod.createExtensionRuntime(); const out={keys:Object.keys(r)};
 try{r.getSessionName()}catch(error){out.unbound=error.message}
 r.registerProvider("fixture",{apiKey:"key"},"extension");
 r.registerNativeProvider({id:"native"},"native-extension");
 out.config=r.pendingProviderRegistrations.map(x=>x.name);
 out.native=r.pendingNativeProviderRegistrations.map(x=>x.provider.id);
 let calls=0;const off=r.trackEventBusSubscription(()=>calls++);off();off();
 r.trackEventBusSubscription(()=>calls++);r.invalidate("stale");r.invalidate("second");
 try{r.assertActive()}catch(error){out.stale=error.message}
 out.calls=calls;return out;
};
const want=await probe(pi),got=await probe(pig);
if(JSON.stringify(got)!==JSON.stringify(want)) throw new Error(JSON.stringify({got,want}));
`)
}

func TestNodeComposedProviderAuthMatchesPi(t *testing.T) {
	runPinnedComparison(t, []string{"dist", "core", "provider-composer.js"}, "../runtime.mjs", `
const [pi, {Runtime}] = await Promise.all([import(process.argv[1]), import(process.argv[2])]);
const config={name:"Fixture",api:"openai-completions",baseUrl:"https://fixture.invalid",apiKey:"literal-key",headers:{"X-Test":"yes"},models:[{id:"one"}]};
const r=new Runtime("provider.mjs");
r.registerProvider("fixture",config);
r.applyModelRegistryState({models:[],providers:{fixture:{name:"Fixture",composed:true}},registered:[{name:"fixture",config}]});
const expected=pi.composeModelProvider("fixture",undefined,{getProvider:()=>undefined},config);
const actual=r.ctx.modelRegistry.getProvider("fixture");
const probe=async p=>({name:p.name,models:p.getModels(),authKeys:Object.keys(p.auth),keyKeys:Object.keys(p.auth.apiKey),check:await p.auth.apiKey.check({ctx:{env:async()=>undefined}}),resolved:await p.auth.apiKey.resolve({ctx:{env:async()=>undefined}})});
const want=await probe(expected),got=await probe(actual);
if(JSON.stringify(got)!==JSON.stringify(want)) throw new Error(JSON.stringify({got,want}));
`)
}

func TestNodeSessionClearedLeafMatchesPi(t *testing.T) {
	runPinnedComparison(t, []string{"dist", "core", "session-manager.js"}, "../runtime.mjs", `
const [pi, {Runtime}] = await Promise.all([import(process.argv[1]), import(process.argv[2])]);
const s=pi.SessionManager.inMemory("/test");
const id=s.appendCustomEntry("note",{value:1});
s.resetLeaf();
const r=new Runtime("leaf.mjs");
r.applyState({session:{sessionId:"s",leafId:id,entriesAppended:s.getEntries(),entryCount:1}});
r.applyState({session:{sessionId:"s",leafId:"",entryCount:1}});
const actual={leaf:r.ctx.sessionManager.getLeafId(),branch:r.ctx.sessionManager.getBranch(),context:r.ctx.sessionManager.buildContextEntries()};
const expected={leaf:s.getLeafId(),branch:s.getBranch(),context:s.buildContextEntries()};
if(JSON.stringify(actual)!==JSON.stringify(expected)) throw new Error(JSON.stringify({actual,expected}));
`)
}

func TestImportedRegistryAndSessionClassesMatchPi(t *testing.T) {
	runPinnedComparison(t, []string{"dist", "core", "session-manager.js"}, "pi-coding-agent.mjs", `
const [pi, pig] = await Promise.all([import(process.argv[1]), import(process.argv[2])]);
const exercise = (mod) => {
 const s = mod.SessionManager.inMemory("/registry-class-test");
 const id = s.appendCustomEntry("note", {n: 1});
 const message = s.appendMessage({role:"user",content:"hello",timestamp:123});
 s.appendLabelChange(message,"chosen");
 s.appendContextEdit(message,{content:"edited"});
 return {cwd:s.getCwd(),dir:s.getSessionDir(),persisted:s.isPersisted(),
  types:s.getEntries().map(e=>e.type), note:s.getEntry(id).data,
  label:s.getLabel(message),messages:s.buildSessionContext().messages,
  branch:s.getBranch(id).map(e=>e.type), roots:s.getTree().map(n=>n.entry.type)};
};
const want=exercise(pi),got=exercise(pig);
if(JSON.stringify(got)!==JSON.stringify(want)) throw new Error(JSON.stringify({want,got}));
const models=[{id:"a",provider:"test"}];
const r=new pig.ModelRegistry({getModels:()=>models,getAvailableSnapshot:()=>models,getModel:()=>models[0],getProvider:()=>({name:"Test"})});
const all=r.getAll(); all.pop();
if(r.getAll().length!==1 || r.find("test","a")!==models[0] || r.getProviderDisplayName("test")!=="Test") throw new Error("ModelRegistry did not delegate or copied incorrectly");
`)
}
