//go:build !pig_strip_node_extensions

package subprocess

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's virtual-modules.ts shares export values; ModelRuntime.prepareRequest throws the same ModelsError exported by pi-ai. Library bundling must not create a second core or import the unbundled catalog as well.
func TestNodeLibraryBundlesKeepSharedIdentitiesWithoutRawCatalogLoads(t *testing.T) {
	root := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node")
	script := `import assert from "node:assert/strict";
import {registerHooks} from "node:module";
import {pathToFileURL} from "node:url";
import {join} from "node:path";
registerHooks({load(url,context,next) {
 if (url.includes("/pi-ai/providers/") && !url.endsWith("/radius-config.js")) assert.fail("unbundled provider/catalog loaded: " + url);
 if (url.endsWith("/pi-ai/models.generated.js") || url.includes("/pi-tui/components/")) assert.fail("unbundled library graph loaded: " + url);
 return next(url,context);
}});
const ai = await import("@earendil-works/pi-ai");
const legacy = await import("@mariozechner/pi-ai/compat");
const all = await import("@earendil-works/pi-ai/providers/all");
const oracle = await import(pathToFileURL(process.argv[3]));
const oracleAll = await import(new URL("./providers/all.js",pathToFileURL(process.argv[3])));
assert.deepEqual(Object.keys(ai),Object.keys(oracle),"public AI export inventory");
assert.equal(ai,legacy);
assert.equal(ai.getModel,all.getBuiltinModel);
assert.equal(ai.getModels,all.getBuiltinModels);
assert.equal(ai.getProviders,all.getBuiltinProviders);
const providers=ai.getProviders();
assert.ok(providers.length>0);
assert.deepEqual(providers,oracle.getProviders());
const models=all.builtinModels();
const oracleModels=oracleAll.builtinModels();
for(const provider of providers) {
 assert.deepEqual(ai.getModels(provider),oracle.getModels(provider));
 for(const model of ai.getModels(provider)) {
  assert.equal(ai.getModel(provider,model.id),model);
  const reference=oracle.getModel(provider,model.id);
  assert.equal(models.getModel(provider,model.id)===model,oracleModels.getModel(provider,model.id)===reference,"catalog/provider identity: "+provider+"/"+model.id);
 }
}
const tui=await import("@earendil-works/pi-tui");
assert.equal(tui,await import("@mariozechner/pi-tui"));
const {stringWidget}=await import(pathToFileURL(join(process.argv[1],"widget-component.mjs")));
const widget=stringWidget(["shared"],{fg:(_token,text)=>text});
assert.ok(widget instanceof tui.Container);
assert.ok(widget.children[0] instanceof tui.Text);
const sdk=await import("@earendil-works/pi-coding-agent");
const runtime=await sdk.ModelRuntime.create({modelsPath:null,authPath:join(process.argv[2],"auth.json"),refreshOnCreate:false});
await assert.rejects(runtime.prepareRequest({provider:"not-registered",id:"none"}),error=>error instanceof ai.ModelsError && error.message==="Unknown provider: not-registered");
`
	oracle := filepath.Join(findModuleRoot(t), "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent", "node_modules", "@earendil-works", "pi-ai", "dist", "compat.js")
	cmd := exec.CommandContext(t.Context(), "node", "--import", registerLoaderURL(t, root), "--input-type=module", "-e", script, root, t.TempDir(), oracle)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("shared library bundles: %v\n%s", err, output)
	}
}

func TestNodeLibraryImportDefersUnusedNativeSegmenters(t *testing.T) {
	root := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node")
	script := `import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
import {join} from "node:path";
let allow=false;
const created=[];
const Segmenter=Intl.Segmenter;
Intl.Segmenter=new Proxy(Segmenter,{construct(target,args,newTarget){
 assert.ok(allow,"unused native segmenter constructed at library import");
 created.push(args[1].granularity);
 return Reflect.construct(target,args,newTarget);
}});
const tui=await import("@earendil-works/pi-tui");
assert.equal(tui.visibleWidth("plain"),5);
assert.deepEqual(created,[]);
allow=true;
const utils=await import(pathToFileURL(join(process.argv[1],"shims/pi-dist/pi-tui/utils.js")));
const grapheme=utils.getGraphemeSegmenter();
const word=utils.getWordSegmenter();
assert.ok(grapheme instanceof Segmenter);
assert.ok(word instanceof Segmenter);
assert.equal(utils.getGraphemeSegmenter(),grapheme);
assert.equal(utils.getWordSegmenter(),word);
assert.deepEqual([...grapheme.segment("e\u0301🙂")].map(x=>x.segment),["e\u0301","🙂"]);
assert.deepEqual([...word.segment("one two")].map(x=>x.segment),["one"," ","two"]);
assert.deepEqual(created,["grapheme","word"]);
assert.equal(tui.visibleWidth("e\u0301🙂"),3);
`
	cmd := exec.CommandContext(t.Context(), "node", "--import", registerLoaderURL(t, root), "--input-type=module", "-e", script, root)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("segmenter import boundary: %v\n%s", err, output)
	}
}

func TestNodeEmojiWidthLoadsTheOriginalExpressionOnlyWhenNeeded(t *testing.T) {
	root := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node")
	script := `import assert from "node:assert/strict";
import {registerHooks} from "node:module";
let allow=false,loaded=false;
registerHooks({load(url,context,next) {
 if(url.endsWith("/pi-tui-emoji.mjs")){assert.ok(allow,"unused Unicode emoji set compiled at import");loaded=true;}
 return next(url,context);
}});
const tui=await import("@earendil-works/pi-tui");
assert.deepEqual(new tui.Text("plain",1,0).render(8),[" plain  "]);
assert.equal(tui.visibleWidth("ascii"),5);
assert.equal(loaded,false);
allow=true;
for(const value of ["😀","🇺🇸","👩🏽‍💻","❤️"])assert.equal(tui.visibleWidth(value),2);
assert.ok(loaded,"emoji width bypassed Pi's RGI expression");
`
	cmd := exec.CommandContext(t.Context(), "node", "--import", registerLoaderURL(t, root), "--input-type=module", "-e", script)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("emoji width import boundary: %v\n%s", err, output)
	}
}
