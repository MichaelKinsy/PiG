package ai

func (p *openAIProvider) getCompatCacheControl(opts StreamOptions) *oaiCacheControl {
	if p.cacheControlFormat() != "anthropic" {
		return nil
	}
	retention := opts.CacheRetention
	if retention == "" {
		retention = CacheRetentionShort
		if getProviderEnvValue("PI_CACHE_RETENTION", mergeProviderEnv(p.cfg.Env, opts.Env)) == "long" {
			retention = CacheRetentionLong
		}
	}
	if retention == CacheRetentionNone {
		return nil
	}
	cc := &oaiCacheControl{Type: "ephemeral"}
	supportsLong := true
	if detected := detectCompat(p.cfg.ProviderID, p.cfg.BaseURL); detected != nil && detected.SupportsLongCacheRetention != nil {
		supportsLong = *detected.SupportsLongCacheRetention
	}
	if retention == CacheRetentionLong && p.compatBool(func(c *OpenAICompat) *bool { return c.SupportsLongCacheRetention }, supportsLong) {
		cc.TTL = "1h"
	}
	return cc
}

// applyAnthropicCacheControl annotates text parts, not messages or images. The conversation breakpoint searches backward past messages without text.
func applyAnthropicCacheControl(messages []oaiMessage, tools *[]oaiTool, cc *oaiCacheControl) {
	for i := range messages {
		if messages[i].Role == "system" || messages[i].Role == "developer" {
			addCacheControlToTextContent(&messages[i], cc)
			break
		}
	}
	if tools != nil && len(*tools) > 0 {
		(*tools)[len(*tools)-1].CacheControl = cc
	}
	for i := len(messages) - 1; i >= 0; i-- {
		switch messages[i].Role {
		case "user", "assistant", "tool":
			if addCacheControlToTextContent(&messages[i], cc) {
				return
			}
		}
	}
}

func addCacheControlToTextContent(message *oaiMessage, cc *oaiCacheControl) bool {
	switch content := message.Content.(type) {
	case string:
		if content == "" {
			return false
		}
		message.Content = []oaiContentPart{{Type: "text", Text: content, CacheControl: cc}}
		return true
	case []oaiContentPart:
		for i := len(content) - 1; i >= 0; i-- {
			if content[i].Type == "text" {
				content[i].CacheControl = cc
				return true
			}
		}
	}
	return false
}
