package sdkscenarios

const (
	// farFuture is an OAuth expiry (2100-01-01) no scenario's clock reaches.
	farFuture = `4102444800000`
	// expired is an OAuth expiry long past.
	expired = `1`
)

// providerObject is a Pi Provider object with both account methods. Its OAuth
// flow reports progress and asks a select prompt; its own API-key login asks for
// the base URL, reports it, then asks for the key and stores both.
func providerObject() Provider {
	return Provider{
		ID:   "conf-sso",
		Name: "Conformance SSO",
		APIKey: &APIKey{Name: "Conformance key", Login: &Flow{
			Steps: []FlowStep{
				{Prompt: `{"type":"text","message":"Proxy base URL:","placeholder":"https://proxy.example"}`, As: "base"},
				{Notify: `{"type":"info","message":"Proxy ${base} accepted"}`},
				{Prompt: `{"type":"secret","message":"Proxy API key:"}`, As: "key"},
			},
			Result: `{"type":"api_key","key":"${key}","env":{"CONF_BASE_URL":"${base}"}}`,
		}},
		OAuth: &OAuth{
			Name: "Conformance account", LoginLabel: "Sign in with Conformance SSO", Subscription: true,
			Login: Flow{
				Steps: []FlowStep{
					{Notify: `{"type":"progress","message":"Contacting SSO"}`},
					{Prompt: `{"type":"select","message":"Choose a team:","options":[{"id":"red","label":"Red team"},{"id":"blue","label":"Blue team"}]}`, As: "team"},
				},
				Result: `{"type":"oauth","access":"token-${team}","refresh":"refresh-${team}","expires":` + farFuture + `}`,
			},
			Refresh: `{"access":"refreshed","expires":` + farFuture + `}`,
		},
	}
}

// ambientProvider declares an API-key method with no login of its own and no
// account login.
func ambientProvider() Provider {
	return Provider{ID: "conf-plain", Name: "Conformance plain", APIKey: &APIKey{Name: "Plain key", CheckSource: "CONF_PLAIN_KEY"}}
}

// oddProvider's API-key login asks a prompt and notifies an event whose type no
// login variant has.
func oddProvider() Provider {
	return Provider{ID: "conf-odd", Name: "Conformance odd", APIKey: &APIKey{Name: "Odd key", Login: &Flow{
		Steps:  []FlowStep{{Prompt: `{"type":"picture","message":"Draw a key"}`, As: "key"}},
		Result: `{"type":"api_key","key":"${key}"}`,
	}}}
}

func loudProvider() Provider {
	return Provider{ID: "conf-loud", Name: "Conformance loud", APIKey: &APIKey{Name: "Loud key", Login: &Flow{
		Steps:  []FlowStep{{Notify: `{"type":"fireworks","message":"Bang"}`}},
		Result: `{"type":"api_key","key":"loud"}`,
	}}}
}

// everyVariantProvider's account login asks every prompt variant and notifies
// every event variant of Pi's closed AuthPrompt and AuthEvent unions
// (packages/ai/src/auth/types.ts).
func everyVariantProvider() Provider {
	return Provider{ID: "conf-all", Name: "Conformance all", OAuth: &OAuth{Name: "All variants", Login: Flow{
		Steps: []FlowStep{
			{Notify: `{"type":"info","message":"Read the notes","links":[{"url":"https://docs.example/login","label":"Notes"}]}`},
			{Notify: `{"type":"auth_url","url":"https://auth.example/start","instructions":"Open the link"}`},
			{Notify: `{"type":"device_code","userCode":"ABCD-EFGH","verificationUri":"https://auth.example/device","intervalSeconds":5,"expiresInSeconds":600}`},
			{Notify: `{"type":"progress","message":"Waiting"}`},
			{Prompt: `{"type":"text","message":"Account name:","placeholder":"acme"}`, As: "account"},
			{Prompt: `{"type":"secret","message":"Account secret:"}`, As: "secret"},
			{Prompt: `{"type":"manual_code","message":"Paste the code:","placeholder":"code"}`, As: "code"},
		},
		Result: `{"type":"oauth","access":"${account}:${secret}","refresh":"${code}","expires":` + farFuture + `}`,
	}}}
}

// orderedProvider's login returns its credential with the keys in an order that
// is neither alphabetical nor the host's fixed order. Pi stores the object the
// login returned and writes it with JSON.stringify (auth-storage.ts:465-467).
func orderedProvider() Provider {
	return Provider{ID: "conf-order", Name: "Conformance order", APIKey: &APIKey{Name: "Order key", Login: &Flow{
		Steps:  []FlowStep{{Prompt: `{"type":"secret","message":"Key:"}`, As: "key"}},
		Result: `{"type":"api_key","env":{"CONF_B":"2","CONF_A":"1"},"key":"${key}"}`,
	}}}
}

// deviceProvider's OAuth login asks the app's device ID twice and keeps both
// answers in the credential.
func deviceProvider() Provider {
	return Provider{ID: "conf-device", Name: "Conformance device", OAuth: &OAuth{
		Name: "Device account",
		Login: Flow{
			Steps:  []FlowStep{{DeviceID: true, As: "first"}, {DeviceID: true, As: "second"}},
			Result: `{"type":"oauth","access":"${first}","refresh":"${second}","expires":` + farFuture + `}`,
		},
	}}
}

func providersAuth() []Scenario {
	const group = "providers-auth"
	both := Spec{Providers: []Provider{providerObject(), ambientProvider()}}
	return []Scenario{
		{
			ID: "methods-survive-registration", Group: group,
			Pi:     "model-runtime.ts registerNativeProvider/composeProvider: the provider object is the composition base, so its OAuth method (with loginLabel) stays offered after the refresh every registration queues; interactive-mode.ts:5893-5895 labels the OAuth option with loginLabel",
			Covers: []string{"call registerProvider"},
			Spec:   both,
			Steps:  []Step{{Op: "settle"}, {Op: "methods", Provider: "conf-sso"}, {Op: "methods", Provider: "conf-plain"}},
			Want: []string{
				`methods conf-sso apiKey="Conformance key" login=true oauth="Conformance account" label="Sign in with Conformance SSO" subscription=true`,
				`methods conf-plain apiKey="Plain key" login=false oauth=none`,
			},
		},
		{
			ID: "provider-without-auth-methods-registers", Group: group,
			Pi:     "packages/ai/src/auth/types.ts:252-255 ProviderAuth: apiKey and oauth are both optional, so a Provider object whose auth is {} is a legal registration",
			Covers: []string{"call registerProvider"},
			Spec:   Spec{Providers: []Provider{{ID: "conf-bare", Name: "Conformance bare"}}},
			Steps:  []Step{{Op: "settle"}, {Op: "methods", Provider: "conf-bare"}},
			Want:   []string{`methods conf-bare apiKey=none oauth=none`},
		},
		{
			ID: "runtime-composes-the-provider-object", Group: group,
			Pi:     "model-runtime.ts getProvider returns the composed provider, whose auth is the provider object's own methods, so a runtime consumer's login offers the OAuth method with its loginLabel",
			Covers: []string{"call registerProvider"},
			Spec:   both,
			Steps:  []Step{{Op: "settle"}, {Op: "runtime-methods", Provider: "conf-sso"}},
			Want: []string{
				`runtime-methods conf-sso apiKey="Conformance key" login=true oauth="Conformance account" label="Sign in with Conformance SSO" subscription=true`,
			},
		},
		{
			ID: "stored-key-resolves-and-checks", Group: group,
			Pi:     "auth/types.ts ApiKeyAuth.resolve/check: a stored credential key resolves (credential.key wins) and check reports the method's own source; models.ts getModels lists the provider's models",
			Covers: []string{"call registerProvider", "object auth.apiKey.resolve", "object auth.apiKey.check"},
			Spec:   both,
			Steps: []Step{
				{Op: "settle"},
				{Op: "models", Provider: "conf-plain"},
				{Op: "check", Provider: "conf-plain"},
				{Op: "setcredential", Provider: "conf-plain", Credential: `{"type":"api_key","key":"sk-stored"}`},
				{Op: "resolve", Provider: "conf-plain"},
			},
			Want: []string{
				`models conf-plain conf-plain-model`,
				`check conf-plain type=api_key source="CONF_PLAIN_KEY"`,
				`resolve conf-plain apiKey="resolved-sk-stored" source="stored credential"`,
			},
		},
		{
			ID: "oauth-login-select", Group: group,
			Pi:     "models.ts login: the OAuth method's login runs with the interaction; a select prompt answers with the option id (auth/types.ts AuthInteraction); the credential the login returned is persisted by credentials.modify",
			Covers: []string{"object auth.oauth.login", "call provider.callback"},
			Spec:   both,
			Steps:  []Step{{Op: "settle"}, {Op: "login", Provider: "conf-sso", Kind: "oauth", Answers: []string{"blue"}}, {Op: "authjson", Provider: "conf-sso"}},
			Want: []string{
				`event {"type":"progress","message":"Contacting SSO"}`,
				`prompt {"type":"select","message":"Choose a team:","options":[{"id":"red","label":"Red team"},{"id":"blue","label":"Blue team"}]}`,
				`login conf-sso oauth credential {"access":"token-blue","expires":` + farFuture + `,"refresh":"refresh-blue","type":"oauth"}`,
				`auth.json conf-sso {"access":"token-blue","expires":` + farFuture + `,"refresh":"refresh-blue","type":"oauth"}`,
			},
		},
		{
			ID: "oauth-login-select-cancel", Group: group,
			Pi:     "models.ts login: a prompt that rejects rejects the flow and nothing is persisted; interactive-mode.ts:6365-6366 shows \"Login cancelled\"",
			Covers: []string{"object auth.oauth.login", "call provider.callback"},
			Spec:   both,
			Steps:  []Step{{Op: "settle"}, {Op: "login", Provider: "conf-sso", Kind: "oauth", Answers: []string{"<cancel>"}, ErrorContains: "Login cancelled"}, {Op: "authjson", Provider: "conf-sso"}},
			Want: []string{
				`event {"type":"progress","message":"Contacting SSO"}`,
				`prompt {"type":"select","message":"Choose a team:","options":[{"id":"red","label":"Red team"},{"id":"blue","label":"Blue team"}]}`,
				`login conf-sso oauth error containing "Login cancelled"`,
				`auth.json conf-sso <absent>`,
			},
		},
		{
			ID: "apikey-own-login", Group: group,
			Pi:     "auth/types.ts ApiKeyAuth.login: the provider's own login prompts for the base URL, notifies, prompts for the key and returns both in the credential; models.ts login persists it",
			Covers: []string{"object auth.apiKey.login", "call provider.callback"},
			Spec:   both,
			Steps:  []Step{{Op: "settle"}, {Op: "login", Provider: "conf-sso", Kind: "api_key", Answers: []string{"https://p.example", "sk-conf"}}, {Op: "authjson", Provider: "conf-sso"}},
			Want: []string{
				`prompt {"type":"text","message":"Proxy base URL:","placeholder":"https://proxy.example"}`,
				`event {"type":"info","message":"Proxy https://p.example accepted"}`,
				`prompt {"type":"secret","message":"Proxy API key:"}`,
				`login conf-sso api_key credential {"env":{"CONF_BASE_URL":"https://p.example"},"key":"sk-conf","type":"api_key"}`,
				`auth.json conf-sso {"env":{"CONF_BASE_URL":"https://p.example"},"key":"sk-conf","type":"api_key"}`,
			},
		},
		{
			ID: "oauth-refresh-persists", Group: group,
			Pi:     "auth/resolve.ts: an expired OAuth credential is refreshed through the provider's own refresh, toAuth supplies the request auth, and the refreshed credential is persisted",
			Covers: []string{"object auth.oauth.refresh", "object auth.oauth.toAuth"},
			Spec:   both,
			Steps: []Step{
				{Op: "settle"},
				{Op: "setcredential", Provider: "conf-sso", Credential: `{"type":"oauth","access":"stale","refresh":"refresh-red","expires":` + expired + `}`},
				{Op: "resolve", Provider: "conf-sso"},
				{Op: "authjson", Provider: "conf-sso"},
			},
			Want: []string{
				`resolve conf-sso apiKey="refreshed" source="OAuth"`,
				`auth.json conf-sso {"access":"refreshed","expires":` + farFuture + `,"refresh":"refresh-red","type":"oauth"}`,
			},
		},
		{
			ID: "unknown-prompt-type-is-an-error", Group: group,
			Pi:     "auth/types.ts AuthPrompt is a closed union; the host answers an unknown prompt type with an error, never a silent cancel or text prompt",
			Covers: []string{"object auth.apiKey.login", "call provider.callback"},
			Spec:   Spec{Providers: []Provider{oddProvider()}},
			Steps:  []Step{{Op: "settle"}, {Op: "login", Provider: "conf-odd", Kind: "api_key", Answers: []string{"x"}, ErrorContains: "unknown login prompt type"}, {Op: "authjson", Provider: "conf-odd"}},
			Want: []string{
				`login conf-odd api_key error containing "unknown login prompt type"`,
				`auth.json conf-odd <absent>`,
			},
		},
		{
			ID: "unknown-event-type-is-an-error", Group: group,
			Pi:     "auth/types.ts AuthEvent is a closed union; the host answers an unknown event type with an error, never progress",
			Covers: []string{"object auth.apiKey.login", "call provider.callback"},
			Spec:   Spec{Providers: []Provider{loudProvider()}},
			Steps:  []Step{{Op: "settle"}, {Op: "login", Provider: "conf-loud", Kind: "api_key", ErrorContains: "unknown login event type"}, {Op: "authjson", Provider: "conf-loud"}},
			Want: []string{
				`login conf-loud api_key error containing "unknown login event type"`,
				`auth.json conf-loud <absent>`,
			},
		},
		{
			ID: "every-prompt-and-event-variant", Group: group,
			Pi:     "auth/types.ts AuthPrompt (text, secret, select, manual_code) and AuthEvent (info with links, auth_url, device_code, progress): each variant reaches the interaction with every field",
			Covers: []string{"object auth.oauth.login", "call provider.callback"},
			Spec:   Spec{Providers: []Provider{everyVariantProvider()}},
			Steps:  []Step{{Op: "settle"}, {Op: "login", Provider: "conf-all", Kind: "oauth", Answers: []string{"acme", "s3cret", "c0de"}}},
			Want: []string{
				`event {"type":"info","message":"Read the notes","links":[{"url":"https://docs.example/login","label":"Notes"}]}`,
				`event {"type":"auth_url","url":"https://auth.example/start","instructions":"Open the link"}`,
				`event {"type":"device_code","userCode":"ABCD-EFGH","verificationUri":"https://auth.example/device","intervalSeconds":5,"expiresInSeconds":600}`,
				`event {"type":"progress","message":"Waiting"}`,
				`prompt {"type":"text","message":"Account name:","placeholder":"acme"}`,
				`prompt {"type":"secret","message":"Account secret:"}`,
				`prompt {"type":"manual_code","message":"Paste the code:","placeholder":"code"}`,
				`login conf-all oauth credential {"access":"acme:s3cret","expires":` + farFuture + `,"refresh":"c0de","type":"oauth"}`,
			},
		},
		{
			ID: "credential-key-order-is-the-logins", Group: group,
			Pi:     "auth-storage.ts:465-467 stores the object the login returned and writes it with JSON.stringify, so auth.json keeps the key order the login produced (observed by parity scenario oauth/30)",
			Covers: []string{"object auth.apiKey.login", "call provider.callback"},
			Spec:   Spec{Providers: []Provider{orderedProvider()}},
			Steps:  []Step{{Op: "settle"}, {Op: "login", Provider: "conf-order", Kind: "api_key", Answers: []string{"k1"}}, {Op: "authjson", Provider: "conf-order", Raw: true}},
			Want: []string{
				`prompt {"type":"secret","message":"Key:"}`,
				`login conf-order api_key credential {"env":{"CONF_A":"1","CONF_B":"2"},"key":"k1","type":"api_key"}`,
				`auth.json conf-order {"type":"api_key","env":{"CONF_B":"2","CONF_A":"1"},"key":"k1"}`,
			},
		},
		{
			ID: "oauth-login-gets-the-device-id", Group: group,
			Pi:     "interactive-mode.ts:6331 passes { getDeviceId } to the login and models.ts:776 hands it to oauth.login(interaction, options) (auth/types.ts:231); every call asks the app (types.ts:202-208)",
			Covers: []string{"object auth.oauth.login", "call provider.callback"},
			Spec:   Spec{Providers: []Provider{deviceProvider()}},
			Steps:  []Step{{Op: "settle"}, {Op: "login", Provider: "conf-device", Kind: "oauth", DeviceID: "dev-42"}},
			Want: []string{
				`login conf-device oauth credential {"access":"dev-42","expires":` + farFuture + `,"refresh":"dev-42","type":"oauth"}`,
				`getDeviceId calls=2`,
			},
		},
		{
			ID: "oauth-login-asks-for-the-device-id-only-when-it-needs-it", Group: group,
			Pi:     "auth/types.ts:202-208: getDeviceId is called only by flows that need it, so the app creates the ID on first use (settings-manager.ts:1198-1205); a flow that never calls it leaves the app's ID uncreated",
			Covers: []string{"object auth.oauth.login"},
			Spec:   both,
			Steps:  []Step{{Op: "settle"}, {Op: "login", Provider: "conf-sso", Kind: "oauth", Answers: []string{"red"}, DeviceID: "dev-42"}},
			Want: []string{
				`event {"type":"progress","message":"Contacting SSO"}`,
				`prompt {"type":"select","message":"Choose a team:","options":[{"id":"red","label":"Red team"},{"id":"blue","label":"Blue team"}]}`,
				`login conf-sso oauth credential {"access":"token-red","expires":` + farFuture + `,"refresh":"refresh-red","type":"oauth"}`,
				`getDeviceId calls=0`,
			},
		},
		{
			ID: "oauth-login-without-a-device-id", Group: group,
			Pi:     "auth/types.ts:202-208: getDeviceId is optional; a login whose app supplies none gives the flow no getDeviceId",
			Covers: []string{"object auth.oauth.login"},
			Spec:   Spec{Providers: []Provider{deviceProvider()}},
			Steps:  []Step{{Op: "settle"}, {Op: "login", Provider: "conf-device", Kind: "oauth"}},
			Want: []string{
				`login conf-device oauth credential {"access":"\u003cnone\u003e","expires":` + farFuture + `,"refresh":"\u003cnone\u003e","type":"oauth"}`,
			},
		},
		{
			ID: "refreshed-credential-keeps-its-key-order", Group: group,
			Pi:     "auth-storage.ts:465-467 stores the credential the provider's refresh returned and writes it with JSON.stringify; every driver's refresh returns { ...credential, ...fields }, so a refreshed key keeps its place and a provider-owned object keeps its own key order",
			Covers: []string{"object auth.oauth.refresh"},
			Spec:   both,
			Steps: []Step{
				{Op: "settle"},
				{Op: "setcredential", Provider: "conf-sso", Credential: `{"type":"oauth","account":{"zone":"z1","id":"a1"},"access":"stale","refresh":"refresh-red","expires":` + expired + `}`},
				{Op: "resolve", Provider: "conf-sso"},
				{Op: "authjson", Provider: "conf-sso", Raw: true},
			},
			Want: []string{
				`resolve conf-sso apiKey="refreshed" source="OAuth"`,
				`auth.json conf-sso {"type":"oauth","account":{"zone":"z1","id":"a1"},"access":"refreshed","refresh":"refresh-red","expires":` + farFuture + `}`,
			},
		},
	}
}
