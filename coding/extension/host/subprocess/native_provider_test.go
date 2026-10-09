//go:build !pig_strip_node_extensions

package subprocess_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
)

type nativeTestUI struct {
	extension.UIContext
	started chan string
}

func (ui *nativeTestUI) Notify(message, level string) { ui.started <- message }

// Pi registers native Providers in its ModelRuntime, not just the extension's
// local object map. Drive the same Services/BuildModel/Stream path as the CLI.
func TestNativeProviderHostLifecycle(t *testing.T) {
	for _, packed := range []bool{false, true} {
		name := "isolated"
		if packed {
			name = "packed"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			entry := filepath.Join(dir, "native.mjs")
			source := `
import {createAssistantMessageEventStream} from "@earendil-works/pi-ai";
import {readFileSync} from "node:fs";
export default pi=>{
 let refreshed=false,failAuth=false,failRefresh=false,waitAuth=false,waitRefresh=false,notify;
 const wait=(signal,label)=>new Promise((resolve,reject)=>{notify(label);signal.addEventListener("abort",()=>reject(new Error("native cancelled "+label)),{once:true})});
 pi.registerCommand("native-control",{handler:async(args,ctx)=>{failAuth=args==="auth";failRefresh=args==="refresh";waitAuth=args==="wait-auth";waitRefresh=args==="wait-refresh";notify=label=>ctx.ui.notify(label)}});
 const model={id:"model",name:"Native",provider:"native-test",api:"openai-completions",baseUrl:"http://127.0.0.1:9",reasoning:false,input:["text"],cost:{input:0,output:0,cacheRead:0,cacheWrite:0},contextWindow:4000,maxTokens:100};
 const stream=(model,context,options)=>{
  if(options.apiKey!=="key"||options.headers?.["X-Auth"]!=="yes")throw new Error("missing auth values");
  const request=context.messages.findLast(m=>m.role==="user")?.content;
  if(request==="throw")throw new Error("native exploded");
  const s=createAssistantMessageEventStream();
  const msg={role:"assistant",api:model.api,provider:model.provider,model:model.id,content:[],usage:{input:0,output:0,cacheRead:0,cacheWrite:0,totalTokens:0,cost:{input:0,output:0,cacheRead:0,cacheWrite:0,total:0}},stopReason:"stop",timestamp:1};
  queueMicrotask(async()=>{
   if(request==="payload"){
    const payload=await options.onPayload({message:"original"},model);
    msg.content=[{type:"text",text:payload.message}];
    s.push({type:"start",partial:structuredClone(msg)});s.push({type:"done",reason:"stop",message:msg});return;
   }
   s.push({type:"start",partial:structuredClone(msg)});
   if(request==="cancel"){
    options.signal.addEventListener("abort",()=>{msg.stopReason="aborted";msg.errorMessage="native cancelled";s.push({type:"error",reason:"aborted",error:msg})},{once:true});return;
   }
   msg.content=[{type:"text",text:"native result"}];s.push({type:"done",reason:"stop",message:msg});
  });return s;
 };
 pi.registerProvider({id:"native-test",name:"Native",baseUrl:model.baseUrl,getModels:()=>refreshed?[model,{...model,id:"new-model"}]:[model],auth:{apiKey:{name:"API key",check:async()=>({type:"api_key",source:"native"}),resolve:async input=>{if(waitAuth)await wait(input.signal,"auth");if(failAuth)throw new Error("native auth exploded");return {auth:{apiKey:"key",headers:{"X-Auth":"yes"}},source:"native"}}}},refreshModels:async ctx=>{if(waitRefresh)await wait(ctx.signal,"refresh");if(failRefresh)throw new Error("native refresh exploded");if(ctx.allowNetwork)await ctx.publish({persist:{models:[{id:"persisted"}]},update:()=>{if(!readFileSync("agent/models-store.json","utf8").includes("persisted"))throw new Error("publication was not persisted before update");refreshed=true}})},stream,streamSimple:stream});
};`
			if err := os.WriteFile(entry, []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: filepath.Join(dir, "agent")})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			host := subprocess.NewHost(dir)
			defer host.Shutdown("done")
			host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
			host.SetNativeProviderCallback(services.Registry().RegisterNativeProvider)
			ui := &nativeTestUI{UIContext: extension.NoopUIContext, started: make(chan string, 1)}
			bridge := subprocess.NewUIBridge(nil)
			bridge.SetUIContext(ui)
			host.SetUIBridge(bridge)
			cfg := subprocess.ExtConfig{Name: "native", Source: entry, Enabled: true}
			var loaded *extension.Extension
			var loadErr error
			if packed {
				loadedSet, errs := host.LoadAll(t.Context(), []subprocess.ExtConfig{cfg})
				loadErr = errors.Join(errs...)
				if len(loadedSet) > 0 {
					loaded = &loadedSet[0]
				}
			} else {
				loaded, loadErr = host.Load(t.Context(), cfg)
			}
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			model := services.ModelRuntime().GetModel("native-test", "model")
			if model == nil {
				t.Fatal("native model absent")
			}
			// The host callbacks are bound straight to the registry, without cmd/pig's binding that starts the queued registration refresh when a callback returns, so this is a direct Go registration (D84): the awaited call runs Pi's local refresh.
			if _, err := services.ModelRuntime().GetAvailable(t.Context()); err != nil {
				t.Fatal(err)
			}
			if !services.Registry().ModelRegistry.HasConfiguredAuth("native-test") {
				t.Fatal("native auth check not used")
			}
			for _, tc := range []struct {
				prompt string
				want   ai.StopReason
				detail string
			}{{"hello", ai.StopReasonStop, ""}, {"throw", ai.StopReasonError, "native exploded"}, {"cancel", ai.StopReasonAborted, "native cancelled"}} {
				t.Run(tc.prompt, func(t *testing.T) {
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					stream := services.ModelRuntime().Stream(ctx, model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText(tc.prompt)}}}, ai.StreamOptions{})
					if tc.prompt == "cancel" {
						for event := range stream.Events(t.Context()) {
							if event.EventType() == ai.EventStart {
								cancel()
								break
							}
						}
					}
					result := stream.Result()
					if result.StopReason != tc.want || !strings.Contains(result.ErrorMessage, tc.detail) {
						t.Fatalf("result: %+v", result)
					}
				})
			}
			refresh := services.Registry().ExtensionRefresh(t.Context(), new(true), []string{"native-test"}, nil)
			if refresh.Aborted || len(refresh.Errors) > 0 {
				t.Fatalf("refresh: %+v", refresh)
			}
			if services.ModelRuntime().GetModel("native-test", "new-model") == nil {
				t.Fatal("refreshed model absent")
			}
			data, err := os.ReadFile(filepath.Join(dir, "agent", "models-store.json"))
			if err != nil {
				t.Fatal(err)
			}
			if !json.Valid(data) || !strings.Contains(string(data), "persisted") {
				t.Fatalf("models not persisted: %s", data)
			}
			payloadCalled := false
			payloadResult := services.ModelRuntime().Complete(t.Context(), model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("payload")}}}, ai.StreamOptions{OnPayload: func(payload any, model *ai.Model) (any, error) {
				payloadCalled = true
				if model.ID != "model" {
					t.Errorf("callback model: %+v", model)
				}
				return map[string]any{"message": "rewritten"}, nil
			}})
			if !payloadCalled || payloadResult.StopReason != ai.StopReasonStop || payloadResult.Content[0].(ai.TextContent).Text != "rewritten" {
				t.Fatalf("payload callback: %+v called=%v", payloadResult, payloadCalled)
			}
			control := loaded.Commands["native-control"]
			for _, kind := range []string{"auth", "refresh"} {
				if err := control.Handler(t.Context(), "wait-"+kind); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error, 1)
				go func() {
					if kind == "auth" {
						_, err := services.Registry().NativeProviderAuth(ctx, "native-test", ai.AuthResolutionOverrides{})
						done <- err
						return
					}
					result := services.Registry().ExtensionRefresh(ctx, new(true), []string{"native-test"}, nil)
					if !result.Aborted {
						done <- errors.New("refresh did not report abort")
					} else {
						done <- context.Canceled
					}
				}()
				if observed := <-ui.started; observed != kind {
					t.Fatalf("callback=%s want %s", observed, kind)
				}
				cancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatalf("cancel %s: %v", kind, err)
				}
			}
			if err := control.Handler(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
			if err := control.Handler(t.Context(), "auth"); err != nil {
				t.Fatal(err)
			}
			if _, err := services.Registry().NativeProviderAuth(t.Context(), "native-test", ai.AuthResolutionOverrides{}); err == nil || !strings.Contains(err.Error(), "native auth exploded") {
				t.Fatalf("auth error: %v", err)
			}
			if err := control.Handler(t.Context(), "refresh"); err != nil {
				t.Fatal(err)
			}
			failed := services.Registry().ExtensionRefresh(t.Context(), new(true), []string{"native-test"}, nil)
			if err := failed.Errors["native-test"]; err == nil || !strings.Contains(err.Error(), "native refresh exploded") {
				t.Fatalf("refresh errors: %+v", failed)
			}
			if err := control.Handler(t.Context(), ""); err != nil {
				t.Fatal(err)
			}
			old := model.Provider
			host.Shutdown("unregister")
			if services.ModelRuntime().GetModel("native-test", "model") != nil {
				t.Fatal("model survived owner shutdown")
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			stale, err := old.Stream(ctx, ai.NormalizeContext(ai.Context{}), ai.StreamOptions{})
			if err == nil && stale.Result().StopReason != ai.StopReasonError {
				t.Fatal("stale provider did not fail")
			}
		})
	}
}
