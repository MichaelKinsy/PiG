//go:build !pig_strip_node_extensions

package extensionconformance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

// Pi's ModelRuntime.prepareRequest keeps the caller's onResponse in the options it hands Provider.stream and Provider.streamSimple (packages/coding-agent/src/core/model-runtime.ts:671-689);
// a Provider awaits it before it answers (packages/ai/src/providers/faux.ts), so the caller's observer sees the response status, headers and initiating model, and an observer's error fails the stream.
// A native Provider registered by an extension must receive the same callback from both stream methods (GitHub issue #206: nativeProviderProxy.stream declared only onPayload and answered every
// provider.callback as onPayload, so the observer was never called and a rejecting observer was skipped).
// mutation-checked: dropping "onResponse" from nativeStreamCallbackNames (coding/extension/host/subprocess/native_provider.go) fails the observer subtests of both stream methods (OnResponse calls = 0, stop reason = stop, the reporter's symptom).
func TestNativeProviderStreamsForwardOnResponse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (starts a Node extension host)")
	}
	dir := t.TempDir()
	entry := filepath.Join(dir, "probe.mjs")
	source := `
import {createAssistantMessageEventStream} from "@earendil-works/pi-ai";
export default pi=>{
 const model={id:"probe-model",name:"Probe",provider:"probe-native",api:"openai-completions",baseUrl:"http://127.0.0.1:9",reasoning:false,input:["text"],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:4000,maxTokens:100};
 const respond=(requested,options)=>{
  const s=createAssistantMessageEventStream();
  const msg={role:"assistant",api:requested.api,provider:requested.provider,model:requested.id,content:[],usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:"stop",timestamp:1};
  (async()=>{
   if(options.onResponse)await options.onResponse({status:207,headers:{"x-probe":"observed"}},requested);
   msg.content=[{type:"text",text:"ok"}];
   s.push({type:"start",partial:structuredClone(msg)});
   s.push({type:"done",reason:"stop",message:msg});
  })().catch(error=>{msg.stopReason="error";msg.errorMessage=String(error.message);s.push({type:"error",reason:"error",error:msg})});
  return s;
 };
 pi.registerProvider({id:"probe-native",name:"Probe",baseUrl:model.baseUrl,getModels:()=>[model],auth:{apiKey:{name:"API key",check:async()=>({type:"api_key",source:"probe"}),resolve:async()=>({auth:{apiKey:"key"}})}},stream:(requested,context,options)=>respond(requested,options),streamSimple:(requested,context,options)=>respond(requested,options)});
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
	if _, err := host.Load(t.Context(), subprocess.ExtConfig{Name: "probe", Source: entry, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel("probe-native", "probe-model")
	if model == nil {
		t.Fatal("the native Provider's model is absent from the catalog")
	}
	request := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("go")}}}
	streams := map[string]func(context.Context, ai.StreamOptions) *ai.AssistantMessageEventStream{
		"Stream": func(ctx context.Context, options ai.StreamOptions) *ai.AssistantMessageEventStream {
			return services.ModelRuntime().Stream(ctx, model, request, options)
		},
		"StreamSimple": func(ctx context.Context, options ai.StreamOptions) *ai.AssistantMessageEventStream {
			return services.ModelRuntime().StreamSimple(ctx, model, request, options)
		},
	}
	for name, stream := range streams {
		t.Run(name+" observer receives the response", func(t *testing.T) {
			var mu sync.Mutex
			var seen []string
			message := stream(t.Context(), ai.StreamOptions{OnResponse: func(_ context.Context, response ai.ProviderResponse, requested *ai.Model) error {
				mu.Lock()
				defer mu.Unlock()
				seen = append(seen, fmt.Sprintf("%d %s %s", response.Status, response.Headers["x-probe"], requested.ID))
				return nil
			}}).Result()
			mu.Lock()
			defer mu.Unlock()
			if len(seen) != 1 || seen[0] != "207 observed probe-model" {
				t.Fatalf("OnResponse calls = %q, want one \"207 observed probe-model\"; stop reason = %s", seen, message.StopReason)
			}
			if message.StopReason != ai.StopReasonStop {
				t.Fatalf("stop reason = %s (%s), want stop", message.StopReason, message.ErrorMessage)
			}
		})
		t.Run(name+" observer error fails the stream", func(t *testing.T) {
			message := stream(t.Context(), ai.StreamOptions{OnResponse: func(context.Context, ai.ProviderResponse, *ai.Model) error {
				return errors.New("observer rejected the response")
			}}).Result()
			if message.StopReason != ai.StopReasonError || !strings.Contains(message.ErrorMessage, "observer rejected the response") {
				t.Fatalf("stop reason = %s, error = %q, want error carrying the observer's message", message.StopReason, message.ErrorMessage)
			}
		})
		t.Run(name+" without an observer", func(t *testing.T) {
			message := stream(t.Context(), ai.StreamOptions{}).Result()
			if message.StopReason != ai.StopReasonStop {
				t.Fatalf("stop reason = %s (%s), want stop", message.StopReason, message.ErrorMessage)
			}
		})
	}
}
