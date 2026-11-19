package codingagent

// A config.oauth provider (registerProvider(name, {oauth})) whose login asks a select prompt and lets the user's cancel
// propagate. Its flow throws when the cancel resolves instead of rejecting, so a resolved cancel shows as a failure.
const configOAuthCancelNodeFixture = `export default pi => {
 pi.registerProvider("cfg-sso", {name:"Cfg SSO", baseUrl:"http://127.0.0.1:9", api:"openai-completions", apiKey:"CFG_SSO_KEY",
  models:[{id:"m", name:"M", reasoning:false, input:["text"], cost:{input:0,output:0,cacheRead:0,cacheWrite:0}, contextWindow:4000, maxTokens:100}],
  oauth:{name:"Cfg SSO", login: async callbacks => {
   const team = await callbacks.onSelect({message:"Choose a team:", options:[{id:"red", label:"Red team"}, {id:"blue", label:"Blue team"}]});
   if (team === undefined) throw new Error("the cancelled select resolved");
   return {access:"t-" + team, refresh:"r", expires:Date.now() + 3600000};
  }, refreshToken: async credentials => credentials, getApiKey: credentials => credentials.access}});
};
`

const configOAuthCancelGoFixture = `package fixture

import "github.com/MichaelKinsy/PiG/extensions/sdk"

func Extension() *sdk.Extension {
	e := sdk.New("cfg-oauth")
	e.RegisterProvider("cfg-sso", sdk.ProviderConfig{
		"name": "Cfg SSO", "baseUrl": "http://127.0.0.1:9", "api": "openai-completions", "apiKey": "CFG_SSO_KEY",
		"models": []map[string]any{{"id": "m", "name": "M", "reasoning": false, "input": []string{"text"}, "cost": map[string]int{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}, "contextWindow": 4000, "maxTokens": 100}},
		"oauth": &sdk.OAuthProvider{Name: "Cfg SSO",
			Login: func(cb *sdk.OAuthLoginCallbacks) (sdk.OAuthCredentials, error) {
				team, err := cb.OnSelect(sdk.OAuthSelectPrompt{Message: "Choose a team:", Options: []sdk.OAuthSelectOption{{ID: "red", Label: "Red team"}, {ID: "blue", Label: "Blue team"}}})
				if err != nil {
					return sdk.OAuthCredentials{}, err
				}
				return sdk.OAuthCredentials{Access: "t-" + team, Refresh: "r"}, nil
			},
			RefreshToken: func(c sdk.OAuthCredentials) (sdk.OAuthCredentials, error) { return c, nil },
			GetAPIKey:    func(c sdk.OAuthCredentials) string { return c.Access },
		},
	})
	return e
}
`
