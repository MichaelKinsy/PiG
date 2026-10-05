# Ollama Native Provider - Status Summary

**Date:** 2026-10-05  
**Status:** Extension validates and loads successfully. Tool calling works via direct Ollama API but needs integration tuning with PiG.

## What Works

✅ **Extension validates**
```bash
pig install ./extensions/ollama-native --validate-only --json
# Result: {"valid": true, "providers": ["ollama-native"], ...}
```

✅ **Provider registers**
```bash
pig -e ./extensions/ollama-native --list-models ollama-native
# Shows: ollama-native/granite-bash-ninja-shadow-3b:latest
```

✅ **Direct Ollama API tool calling works**
```bash
curl http://localhost:11434/api/chat \
  -d '{"model":"granite-bash-ninja-shadow-3b:latest","messages":[{"role":"user","content":"Read /etc/hostname"}],"tools":[{"type":"function","function":{"name":"read","parameters":{...}}}],"stream":false}'
# Returns: {"tool_calls":[{"function":{"name":"read","arguments":{"path":"/etc/hostname"}}}]}
```

⚠️ **PiG integration needs tuning**
- Extension loads and responds
- Tools may need to be extracted from a different request field
- Content format conversion is working (array → string)

## Files Created

| File | Purpose |
|------|---------|
| `extension.go` | Main extension code |
| `go.mod` | Go module definition |
| `README.md` | Documentation |
| `SUMMARY.md` | This file |

## Next Steps for Full Integration

1. **Debug tool passing**: PiG may send tools in `request["tools"]` or a nested structure
2. **Test with working model**: granite-bash-ninja supports tool calling
3. **Verify response format**: Ensure tool_calls are in correct PiG format

## Workaround (Current)

Use the Python wrapper for tool calling:
```bash
pig-ollama-native granite-bash-ninja-shadow-3b:latest "Read /etc/hostname" \
  --tool '{"type":"function","function":{"name":"read","description":"Read a file","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}}'
```

## PR to Upstream

When ready, create PR to PiG upstream:
1. Fork https://github.com/MichaelKinsy/PiG
2. Branch: `feature/ollama-native-provider`
3. Commit with signed-off-by
4. Push and open PR as peder1981
