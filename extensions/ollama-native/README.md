# Ollama Native Extension for PiG

A PiG extension that registers an `ollama-native` provider using Ollama's native `/api/chat` endpoint for tool calling support.

## Features
- Uses Ollama native API instead of OpenAI-compatible endpoint
- Supports tool calling with models that have been trained for it
- Works with `granite-bash-ninja-shadow-3b:latest`

## Installation
```bash
pig install ./extensions/ollama-native
```

## Usage
```bash
# List models
pig --list-models ollama-native

# Use the provider
pig --model ollama-native/granite-bash-ninja-shadow-3b:latest "Your prompt"
```

## Tool Calling
The extension sends PiG tools to Ollama in the correct format. When the model is prompted to use a tool, it returns a proper `tool_calls` array that PiG can execute.

### Example
```bash
pig --extension ./extensions/ollama-native --model ollama-native/granite-bash-ninja-shadow-3b:latest -p "Use calculator to compute 5*3"
```

## Technical Details
- **Endpoint**: `http://localhost:11434/api/chat`
- **Format**: Ollama native (not OpenAI-compatible)
- **Tool Format**: `{"id": "...", "function": {"name": "...", "arguments": {...}}}`

## Requirements
- Ollama running on `localhost:11434`
- Model with tool calling support (e.g., `granite-bash-ninja-shadow-3b:latest`)
