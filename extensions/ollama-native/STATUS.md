# Ollama Native Extension - Status

## Summary
Successfully implemented a PiG extension with streaming support for Ollama's native `/api/chat` endpoint. **Deployed to Proxmox Homelab.**

## What Works
- ✅ Extension validates and installs correctly
- ✅ Provider registration works (`ollama-native`)
- ✅ Model listing works (`pig --list-models ollama-native`)
- ✅ Streaming text responses work
- ✅ Multiple models tested successfully
- ✅ **Deployed to Proxmox Homelab** (not LXC101)

## Tested Models
| Model | Laptop | Homelab | Notes |
|-------|--------|---------|-------|
| granite4.1:3b | ✅ Working | ✅ Working | Best tool calling model |
| minicpm5-1b-uncensored:latest | ✅ Working | ⏳ Not pulled | Good general purpose |
| lfm25-1b-uncensored:latest | ✅ Working | ⏳ Not pulled | Fast responses |

## Tool Calling Results
- **Laptop**: "Use calculator to compute 5*3" → `15`
- **Homelab**: "Use calculator to compute 7*8" → Working (needs tool execution)
- **Homelab**: "Hello" → "Hello! How can I help you today?"

## Deployment to Proxmox Homelab

### Steps Completed
```bash
# 1. Install Go (already present: go1.24.4)
ssh homelab "go version"

# 2. Install Pig
ssh homelab "curl -L -o /tmp/pig.tar.gz https://github.com/MichaelKinsy/PiG/releases/download/v0.4.0/pig-0.4.0-linux-amd64.tar.gz"
ssh homelab "tar xzf /tmp/pig.tar.gz -C /tmp"
ssh homelab "sudo cp /tmp/pig-0.4.0-linux-amd64/pig /usr/local/bin/pig"

# 3. Copy extension
scp -r /home/peder/Projetos/PiG/extensions/ollama-native homelab:/tmp/
ssh homelab "mkdir -p ~/.pig/extensions && cp -r /tmp/ollama-native ~/.pig/extensions/"

# 4. Configure provider in models.json
ssh homelab "cat > ~/.pig/agent/models.json << 'EOF'
{
  'providers': {
    'ollama-native': {
      'baseUrl': 'http://localhost:11434',
      'api': 'ollama-native',
      'apiKey': 'ollama',
      'models': [...]
    }
  }
}
EOF"

# 5. Pull model
ssh homelab "ollama pull granite4.1:3b"

# 6. Test
ssh homelab "pig -e /root/.pig/extensions/ollama-native --model ollama-native/granite4.1:3b -p 'Hello'"
```

### Test Results
- ✅ Extension loaded successfully
- ✅ Model responded to "Hello" → "Hello! How can I help you today?"
- ✅ Tool calling works at API level

## Key Findings

### Ollama Native API Format
```json
{
  "tool_calls": [
    {
      "id": "call_xxx",
      "function": {
        "name": "calculator",
        "arguments": {"expr": "5*3"}
      }
    }
  ]
}
```

### Streaming Format
Each chunk is a separate JSON line:
```json
{"model":"...","message":{"role":"assistant","content":"Hello"},"done":false}
{"model":"...","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop"}
```

### PiG Event Format
- `start` with `partial` containing `content: []`
- `text_start/delta/end` with `contentIndex` and `partial`
- `toolcall_start/delta/end` with `contentIndex`, `id`, `toolName`, `toolCall`, and `partial`
- `done` with `reason` and `message`

## Files
- `extension.go` - Main extension code with streaming
- `go.mod` - Go module definition
- `README.md` - Documentation
- `STATUS.md` - This file

## Next Steps
1. ✅ Add streaming support - DONE
2. ✅ Test with more models - DONE
3. ✅ Deploy to Proxmox Homelab - DONE
4. Submit PR to upstream PiG
