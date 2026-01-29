# Heimdall AI Assistant

Heimdall is NornicDB's built-in AI assistant that enables natural language interaction with your graph database. Access it through the Bifrost chat interface in the admin UI.

## Quick Start

### Enable Heimdall

```bash
# Environment variable (overrides config file)
export NORNICDB_HEIMDALL_ENABLED=true

# Or in docker-compose
environment:
  NORNICDB_HEIMDALL_ENABLED: "true"

# Start NornicDB
./nornicdb serve
```

Or via config file:

```yaml
heimdall:
  enabled: true
```

**Precedence note:** environment variables override YAML config values. If `heimdall.enabled: false` “does nothing”, check whether `NORNICDB_HEIMDALL_ENABLED` is set in your container/runtime environment. At startup you’ll see `→ Provider: openai|ollama|local` so you can confirm which backend was chosen; ensure env vars are exported in the same shell (or process) before starting.

#### Using Ollama (no GGUF download)

If you run [Ollama](https://ollama.com) locally, point Heimdall at it:

```bash
export NORNICDB_HEIMDALL_ENABLED=true
export NORNICDB_HEIMDALL_PROVIDER=ollama
# Optional: export NORNICDB_HEIMDALL_API_URL=http://localhost:11434
# Optional: export NORNICDB_HEIMDALL_MODEL=llama3.2
./nornicdb serve
```

#### Using OpenAI (or compatible API)

Use OpenAI or an OpenAI-compatible endpoint (e.g. Azure OpenAI, local proxy):

```bash
export NORNICDB_HEIMDALL_ENABLED=true
export NORNICDB_HEIMDALL_PROVIDER=openai
export NORNICDB_HEIMDALL_API_KEY=sk-your-key
# Optional: export NORNICDB_HEIMDALL_API_URL=https://api.openai.com
# Optional: export NORNICDB_HEIMDALL_MODEL=gpt-4o-mini
./nornicdb serve
```

#### Running via script or make

When you start NornicDB from a script or `make`, the process only sees env vars set in that same invocation. Set **all** Heimdall (and embedding) vars in the same env block so they override any config file:

```bash
NORNICDB_HEIMDALL_ENABLED=true \
  NORNICDB_HEIMDALL_PROVIDER=openai \
  NORNICDB_HEIMDALL_API_KEY=$OPENAI_API_KEY \
  NORNICDB_HEIMDALL_PLUGINS_DIR=plugins/heimdall/built-plugins \
  NORNICDB_PLUGINS_DIR=apoc/built-plugins \
  NORNICDB_MODELS_DIR=models \
  NORNICDB_EMBEDDING_PROVIDER=local \
  NORNICDB_EMBEDDING_MODEL=bge-m3 \
  NORNICDB_EMBEDDING_DIMENSIONS=1024 \
  ./nornicdb serve
```

For local GGUF instead of OpenAI, use `NORNICDB_HEIMDALL_PROVIDER=local` (or omit it) and omit `NORNICDB_HEIMDALL_API_KEY`.

### Access Bifrost Chat

1. Open NornicDB admin UI at `http://localhost:7474`
2. Click the AI Assistant icon (helmet) in the top bar
3. The Bifrost chat panel opens on the right

## Configuration

| Environment Variable | Default | Description |
|---------------------|---------|-------------|
| `NORNICDB_HEIMDALL_ENABLED` | `false` | Enable/disable the AI assistant |
| `NORNICDB_HEIMDALL_PROVIDER` | `local` | Backend: `local` (GGUF), `ollama`, or `openai` |
| `NORNICDB_HEIMDALL_API_URL` | (see below) | API base URL for ollama/openai |
| `NORNICDB_HEIMDALL_API_KEY` | (empty) | API key for OpenAI (required when provider=openai) |
| `NORNICDB_HEIMDALL_MODEL` | `qwen3-0.6b-instruct` | Model name (GGUF file, Ollama model, or OpenAI model) |
| `NORNICDB_MODELS_DIR` | `/app/models` | Directory containing GGUF models (local only) |
| `NORNICDB_HEIMDALL_GPU_LAYERS` | `-1` | GPU layers (-1 = auto, local only) |
| `NORNICDB_HEIMDALL_CONTEXT_SIZE` | `8192` | Context window (tokens, local only) |
| `NORNICDB_HEIMDALL_BATCH_SIZE` | `2048` | Batch size for prefill (local only) |
| `NORNICDB_HEIMDALL_MAX_TOKENS` | `1024` | Max tokens per response |
| `NORNICDB_HEIMDALL_TEMPERATURE` | `0.5` | Response creativity (0.0-1.0). Default 0.5 aligns with Qwen3 0.6B instruct best practices to reduce repetition; use 0.1 for more deterministic output. |

For detailed information about context handling and token budgets, see [Heimdall Context & Tokens](./heimdall-context.md). For **Qwen3 0.6B instruct**, defaults use temperature 0.5, top_p 0.8, top_k 20 and explicit "do not repeat" instructions in the system prompt to avoid repeated lines. When using **Ollama or OpenAI**, you can safely increase token budgets (e.g. 32K or 128K context); that guide includes example env values.

### Provider: local / ollama / openai

Heimdall supports the same BYOM/ollama/OpenAI style as the embedding subsystem:

| Provider | Description | API URL default | API key |
|----------|-------------|-----------------|---------|
| **local** | Load a GGUF model from `NORNICDB_MODELS_DIR` (BYOM). | N/A | N/A |
| **ollama** | Use Ollama’s `/api/chat`; no API key. | `http://localhost:11434` | Not used |
| **openai** | Use OpenAI (or compatible) chat completions API. | `https://api.openai.com` | **Required** |

**Environment variables by provider:**

- **local**: `NORNICDB_HEIMDALL_PROVIDER=local` (or unset), `NORNICDB_HEIMDALL_MODEL`, `NORNICDB_MODELS_DIR`, `NORNICDB_HEIMDALL_GPU_LAYERS`, etc.
- **ollama**: `NORNICDB_HEIMDALL_PROVIDER=ollama`, optional `NORNICDB_HEIMDALL_API_URL` (default `http://localhost:11434`), optional `NORNICDB_HEIMDALL_MODEL` (e.g. `llama3.2`).
- **openai**: `NORNICDB_HEIMDALL_PROVIDER=openai`, `NORNICDB_HEIMDALL_API_KEY` (required), optional `NORNICDB_HEIMDALL_API_URL` and `NORNICDB_HEIMDALL_MODEL` (default `gpt-4o-mini`).

**YAML examples:**

```yaml
# Local GGUF (default)
heimdall:
  enabled: true
  provider: local
  model: qwen2.5-1.5b-instruct-q4_k_m

# Ollama
heimdall:
  enabled: true
  provider: ollama
  api_url: "http://localhost:11434"
  model: llama3.2

# OpenAI (or compatible)
heimdall:
  enabled: true
  provider: openai
  api_url: "https://api.openai.com"
  api_key: "sk-..."
  model: gpt-4o-mini
```

### OpenAI with larger context (defaults + 128K)

The default token budget is 8K total context. For **OpenAI** (and Ollama), inference runs on the provider, so you can use larger context windows (e.g. 128K for GPT-4o-mini) without hitting local GPU limits. Set the token-budget env vars and run with your usual defaults:

```bash
NORNICDB_HEIMDALL_MAX_CONTEXT_TOKENS=131072 \
NORNICDB_HEIMDALL_MAX_SYSTEM_TOKENS=100000 \
NORNICDB_HEIMDALL_MAX_USER_TOKENS=30000 \
NORNICDB_HEIMDALL_PROVIDER=openai \
NORNICDB_HEIMDALL_API_KEY=$OPENAI_API_KEY \
NORNICDB_HEIMDALL_MODEL=gpt-4o-mini \
NORNICDB_HEIMDALL_ENABLED=true \
NORNICDB_HEIMDALL_PLUGINS_DIR=plugins/heimdall/built-plugins \
NORNICDB_PLUGINS_DIR=apoc/built-plugins \
NORNICDB_MODELS_DIR=models \
NORNICDB_EMBEDDING_PROVIDER=local \
NORNICDB_EMBEDDING_MODEL=bge-m3 \
NORNICDB_EMBEDDING_DIMENSIONS=1024 \
NORNICDB_DATA_DIR=./data/test \
NORNICDB_KMEANS_CLUSTERING_ENABLED=true \
./bin/nornicdb serve --no-auth
```

Startup logs will show the active budget, e.g. `Token budget: 128K context = 100K system + 30K user`. For 32K context examples, see [Heimdall Context & Tokens](./heimdall-context.md).

**Streaming:** Bifrost supports streaming responses (SSE). When the client requests `stream: true`, the OpenAI and Ollama providers stream tokens as they are generated; the local (GGUF) provider also supports streaming when built with the appropriate backend.

## Available Commands

### Built-in Commands (Bifrost UI)

| Command | Description |
|---------|-------------|
| `/help` | Show available commands |
| `/clear` | Clear chat history |
| `/status` | Show connection status |
| `/model` | Show current model |

### Actions and MCP (Model Context Protocol)

Heimdall actions follow the same shape as [MCP tools](https://modelcontextprotocol.io): each action has a **name**, **description**, and optional **inputSchema** (JSON Schema for parameters). Invocation uses action name plus **params** (equivalent to MCP `arguments`). Use `heimdall.ActionsAsMCPTools()` to export all registered actions in MCP tool list format for MCP clients or to merge with NornicDB’s MCP server tool list.

### Natural Language Actions

Ask Heimdall in plain English:

| Request | What it does |
|---------|--------------|
| "get status" | Show database and system status |
| "db stats" | Show node/relationship counts |
| "hello" | Test connection with greeting |
| "show metrics" | Runtime metrics (memory, goroutines) |
| "health check" | System health status |

### Query Examples

```
count all nodes
show database statistics  
what labels exist in the database
```

## API Endpoints

Bifrost provides OpenAI-compatible HTTP endpoints:

```bash
# Check status
curl http://localhost:7474/api/bifrost/status

# Chat (single message)
curl -X POST http://localhost:7474/api/bifrost/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"messages": [{"role": "user", "content": "get status"}]}'

# Stream response
curl -X POST http://localhost:7474/api/bifrost/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"messages": [{"role": "user", "content": "hello"}], "stream": true}'
```

## Docker Deployment

### Pre-built Image (Recommended)

The `nornicdb-arm64-metal-bge-heimdall` image includes Heimdall ready to use:

```bash
docker pull timothyswt/nornicdb-arm64-metal-bge-heimdall:latest

docker run -d \
  -p 7474:7474 \
  -p 7687:7687 \
  -v nornicdb-data:/data \
  timothyswt/nornicdb-arm64-metal-bge-heimdall
```

### BYOM (Bring Your Own Model)

Heimdall supports any instruction-tuned GGUF model. You can use different models for different use cases.

#### Supported Models

| Model | Size | Speed | Quality | Use Case |
|-------|------|-------|---------|----------|
| `qwen3-0.6b-instruct` | 469 MB | Fast | Basic | Quick commands, low memory |
| `qwen2.5-1.5b-instruct-q4_k_m` | 1.0 GB | Medium | Good | **Recommended** - balanced |
| `qwen2.5-3b-instruct-q4_k_m` | 2.0 GB | Slower | Better | Complex queries |
| `phi-3-mini-4k-instruct` | 2.3 GB | Medium | Good | Alternative option |
| `llama-3.2-1b-instruct` | 1.3 GB | Medium | Good | Llama alternative |

#### Download a Model

```bash
# From Hugging Face (Qwen 1.5B recommended)
curl -L -o models/qwen2.5-1.5b-instruct-q4_k_m.gguf \
  "https://huggingface.co/Qwen/Qwen2.5-1.5B-Instruct-GGUF/resolve/main/qwen2.5-1.5b-instruct-q4_k_m.gguf"

# Smaller model (faster, less capable)
curl -L -o models/qwen3-0.6b-instruct.gguf \
  "https://huggingface.co/Qwen/qwen3-0.6b-Instruct-GGUF/resolve/main/qwen3-0.6b-instruct-q4_k_m.gguf"

# Larger model (slower, more capable)
curl -L -o models/qwen2.5-3b-instruct-q4_k_m.gguf \
  "https://huggingface.co/Qwen/Qwen2.5-3B-Instruct-GGUF/resolve/main/qwen2.5-3b-instruct-q4_k_m.gguf"
```

#### Docker with Custom Model

```bash
docker run -d \
  -p 7474:7474 \
  -p 7687:7687 \
  -v nornicdb-data:/data \
  -v /path/to/models:/app/models \
  -e NORNICDB_HEIMDALL_ENABLED=true \
  -e NORNICDB_HEIMDALL_MODEL=your-model-name \
  timothyswt/nornicdb-arm64-metal-bge
```

#### Local Development

```bash
# Set models directory
export NORNICDB_MODELS_DIR=./models
export NORNICDB_HEIMDALL_ENABLED=true
export NORNICDB_HEIMDALL_MODEL=qwen2.5-1.5b-instruct-q4_k_m

# Start NornicDB
./nornicdb serve
```

#### Model Naming Convention

The model name should match the filename without `.gguf`:

```
File: models/qwen2.5-1.5b-instruct-q4_k_m.gguf
ENV:  NORNICDB_HEIMDALL_MODEL=qwen2.5-1.5b-instruct-q4_k_m
```

#### Choosing a Quantization

GGUF models come in different quantizations (compression levels):

| Quantization | Quality | Size | Speed |
|--------------|---------|------|-------|
| `q4_k_m` | Good | ~40% | Fast | **Recommended** |
| `q5_k_m` | Better | ~50% | Medium |
| `q8_0` | Best | ~80% | Slower |
| `f16` | Original | 100% | Slowest |

For Heimdall, `q4_k_m` provides the best balance of quality and performance.

#### GPU vs CPU

```bash
# Auto-detect GPU (recommended)
export NORNICDB_HEIMDALL_GPU_LAYERS=-1

# Force all layers on GPU
export NORNICDB_HEIMDALL_GPU_LAYERS=999

# Force CPU only
export NORNICDB_HEIMDALL_GPU_LAYERS=0
```

On Apple Silicon, Metal acceleration is automatic. On NVIDIA, CUDA is used if available.

## Disabling Heimdall

To run NornicDB without the AI assistant:

```bash
# Don't set the variable (disabled by default)
./nornicdb serve

# Or explicitly disable
NORNICDB_HEIMDALL_ENABLED=false ./nornicdb serve
```

When disabled:
- Bifrost chat UI shows "AI Assistant not enabled"
- `/api/bifrost/*` endpoints return disabled status
- No SLM model is loaded (saves memory)
- Heimdall plugins are **not** started (they are skipped unless Heimdall is enabled and initialized)

## Chat History

- Chat history persists while the browser session is open
- Closing and reopening Bifrost preserves history
- Closing the browser tab clears history
- Use `/clear` command to manually clear

## Troubleshooting

### "AI Assistant is not enabled"

```bash
# Verify environment variable
echo $NORNICDB_HEIMDALL_ENABLED

# Check startup logs for:
# ✅ Heimdall AI Assistant ready
#    → Model: qwen2.5-1.5b-instruct-q4_k_m
```

### "Model not found"

```bash
# Check models directory
ls /app/models/  # In container
ls ./models/     # Local

# Set correct path
export NORNICDB_MODELS_DIR=/path/to/models
```

### Slow Responses

- Try a smaller model (0.5B instead of 1.5B)
- Enable GPU acceleration: `NORNICDB_HEIMDALL_GPU_LAYERS=-1`
- Reduce max tokens: `NORNICDB_HEIMDALL_MAX_TOKENS=256`

### Actions Not Executing

The SLM interprets your request and outputs action commands. If actions don't execute:

1. Try simpler phrasing: "get status" instead of "what's the current status of everything"
2. Use exact action names: "db stats", "hello", "health"
3. Check server logs for `[Bifrost]` messages

## Extending Heimdall

Create custom plugins to add new capabilities:

- [Writing Heimdall Plugins](./heimdall-plugins.md)
- [Plugin Architecture](../architecture/COGNITIVE_SLM_PROPOSAL.md)

### Plugin Features

Heimdall plugins support advanced features:

#### Lifecycle Hooks

Plugins can implement optional interfaces to hook into the request lifecycle:

| Hook | When Called | Use Case |
|------|-------------|----------|
| `PrePromptHook` | Before SLM request | Modify prompts, add context, validate |
| `PreExecuteHook` | Before action execution | Validate params, fetch data, authorize |
| `PostExecuteHook` | After action execution | Logging, metrics, cleanup |
| `DatabaseEventHook` | On database operations | Audit, monitoring, triggers |

#### Autonomous Actions

Plugins can trigger SLM actions based on accumulated events:

```go
// Example: Trigger analysis after multiple failures
func (p *SecurityPlugin) OnDatabaseEvent(event *heimdall.DatabaseEvent) {
    if event.Type == heimdall.EventQueryFailed {
        p.failureCount++
        if p.failureCount >= 5 {
            // Directly invoke an action
            p.ctx.Heimdall.InvokeActionAsync("heimdall.anomaly.detect", nil)
            
            // Or send a natural language prompt
            p.ctx.Heimdall.SendPromptAsync("Analyze recent query failures")
        }
    }
}
```

#### Inline Notifications

Plugin notifications appear in proper order within the chat stream:

```go
func (p *MyPlugin) PrePrompt(ctx *heimdall.PromptContext) error {
    ctx.NotifyInfo("Processing", "Analyzing your request...")
    return nil
}
```

Notifications from lifecycle hooks are queued and sent inline with the streaming response, ensuring proper ordering.

#### Request Cancellation

Plugins can cancel requests with a reason:

```go
func (p *MyPlugin) PrePrompt(ctx *heimdall.PromptContext) error {
    if !p.isAuthorized(ctx.UserMessage) {
        ctx.Cancel("Unauthorized request", "PrePrompt:myplugin")
        return nil
    }
    return nil
}
```

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────┐
│  User: "Check database status"                                  │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Bifrost (Chat Interface)                                       │
│  └─ Creates PromptContext                                       │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  PrePrompt Hooks                                                │
│  └─ Plugins can modify prompt, add context, or cancel           │
│  └─ Notifications queued for inline delivery                    │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Heimdall SLM                                                   │
│  └─ Interprets user intent                                      │
│  └─ Outputs: {"action": "heimdall_watcher_status", "params": {}}│
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  PreExecute Hooks                                               │
│  └─ Plugins can validate/modify params or cancel                │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Action Execution                                               │
│  └─ Registered handler executes (heimdall_watcher_status)       │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  PostExecute Hooks                                              │
│  └─ Plugins receive result, can log and send notifications      │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  Response streamed to user with inline notifications            │
│  [Heimdall]: ✅ Watcher: Action completed in 1.23ms             │
│  {"status": "running", "goroutines": 35, ...}                   │
└─────────────────────────────────────────────────────────────────┘
```

---

**See Also:**
- [Configuration Reference](../configuration/)
- [Docker Deployment](../getting-started/)
- [API Reference](../api-reference/)
