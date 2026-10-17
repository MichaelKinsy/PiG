//go:build !pig_strip_node_extensions

package subprocess_test

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// TestNativeProviderSessionRequestHooks drives a session turn through a Provider object. Pi's session passes onPayload, onResponse and onProviderStreamEvent to modelRuntime.streamSimple (sdk.ts:385-433), whose prepareRequest hands them to Provider.stream (model-runtime.ts:671-689), so a Provider that reports its response and stream events emits after_provider_response and provider_stream_event, and before_provider_request replaces its payload.
func TestNativeProviderSessionRequestHooks(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "hooks.mjs")
	source := `
import {createAssistantMessageEventStream} from "@earendil-works/pi-ai";
export default pi=>{
 const model={id:"hooks-model",name:"Hooks",provider:"hooks-native",api:"openai-completions",baseUrl:"http://127.0.0.1:9",reasoning:false,input:["text"],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:4000,maxTokens:100};
 const streamSimple=(requested,context,options)=>{
  const s=createAssistantMessageEventStream();
  const msg={role:"assistant",api:requested.api,provider:requested.provider,model:requested.id,content:[],usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:"stop",timestamp:1};
  (async()=>{
   if(typeof options.transformHeaders!=="undefined")throw new Error("Provider.stream received transformHeaders");
   const payload=await options.onPayload({body:"original"},requested);
   await options.onResponse({status:209,headers:{"x-native":"response"}},requested);
   await options.onProviderStreamEvent({raw:"chunk"},requested);
   msg.content=[{type:"text",text:payload.body}];
   s.push({type:"start",partial:structuredClone(msg)});
   s.push({type:"done",reason:"stop",message:msg});
  })().catch(error=>{msg.stopReason="error";msg.errorMessage=String(error.message);s.push({type:"error",reason:"error",error:msg})});
  return s;
 };
 pi.registerProvider({id:"hooks-native",name:"Hooks",baseUrl:model.baseUrl,getModels:()=>[model],auth:{apiKey:{name:"API key",check:async()=>({type:"api_key",source:"hooks"}),resolve:async()=>({auth:{apiKey:"key"}})}},stream:streamSimple,streamSimple});
};`
	if err := os.WriteFile(entry, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	host := subprocess.NewHost(dir)
	t.Cleanup(func() { host.Shutdown("test done") })
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	host.SetNativeProviderCallback(services.Registry().RegisterNativeProvider)
	host.SetUIBridge(subprocess.NewUIBridge(nil))
	loaded, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "hooks", Source: entry, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("hooks-native", "hooks-model")
	if model == nil {
		t.Fatal("the Provider object's model is absent from the catalog")
	}
	var mu sync.Mutex
	var seen []string
	record := func(entry string) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, entry)
	}
	observer := extension.Extension{Path: "/observer", Handlers: map[string][]extension.HandlerFn{
		"before_provider_request": {func(args ...any) (any, error) {
			event := args[0].(extension.BeforeProviderRequestEvent)
			record("before_provider_request")
			payload, _ := event.Payload.(map[string]any)
			if payload["body"] != "original" {
				t.Errorf("before_provider_request payload = %#v", event.Payload)
			}
			return map[string]any{"body": "replaced by hook"}, nil
		}},
		"after_provider_response": {func(args ...any) (any, error) {
			event := args[0].(extension.AfterProviderResponseEvent)
			record("after_provider_response")
			if event.Status != 209 || event.Headers["x-native"] != "response" {
				t.Errorf("after_provider_response = %#v", event)
			}
			return nil, nil
		}},
		"provider_stream_event": {func(args ...any) (any, error) {
			record("provider_stream_event")
			return nil, nil
		}},
	}}
	runner := inproc.NewRunner([]extension.Extension{*loaded, observer}, dir)
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range session.Events() {
		}
	}()
	t.Cleanup(func() {
		_ = session.Close()
		<-drained
	})
	if _, err := session.Send(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	messages := session.Messages()
	last := messages[len(messages)-1].Assistant
	if last == nil || last.StopReason != ai.StopReasonStop || len(last.Content) != 1 || last.Content[0].(ai.TextContent).Text != "replaced by hook" {
		t.Fatalf("assistant message = %#v", last)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := []string{"before_provider_request", "after_provider_response", "provider_stream_event"}; !slices.Equal(seen, want) {
		t.Fatalf("extension events = %q, want %q", seen, want)
	}
}
