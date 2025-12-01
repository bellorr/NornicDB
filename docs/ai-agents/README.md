# AI Agents & Integration

**Integrate NornicDB with AI agents and tools.**

## 📚 Documentation

- **[Chat Modes](chat-modes.md)** - Use with Cursor IDE and AI assistants
- **[MCP Tools Reference](mcp-tools-reference.md)** - Model Context Protocol tools
- **[Cursor Integration](cursor-integration.md)** - Cursor IDE setup
- **[Agent Examples](agent-examples.md)** - Agent patterns and examples

## 🤖 AI Integration Features

### Model Context Protocol (MCP)
NornicDB provides 6 MCP tools for AI agents:
- `search` - Semantic search
- `cypher` - Execute queries
- `schema` - Get graph schema
- `neighbors` - Find related nodes
- `path` - Find paths between nodes
- `stats` - Get statistics

[MCP Tools Reference →](mcp-tools-reference.md)

### Cursor IDE Integration
Use NornicDB as a knowledge base for your codebase.

[Cursor Setup Guide →](cursor-integration.md)

### Chat Modes
Three modes for different use cases:
- **Ask** - Question answering
- **Search** - Information retrieval
- **Analyze** - Data analysis

[Chat Modes Guide →](chat-modes.md)

## 🚀 Quick Start

### With Cursor IDE

1. Install NornicDB MCP server
2. Configure in Cursor settings
3. Start chatting with your knowledge base

[Complete setup →](cursor-integration.md)

### With Custom Agents

```python
from nornicdb import MCP Client

client = MCPClient("http://localhost:7474")

# Search knowledge base
results = client.search("machine learning algorithms")

# Execute Cypher
data = client.cypher("MATCH (n:Concept) RETURN n LIMIT 10")
```

[Agent examples →](agent-examples.md)

## 📖 Learn More

- **[MCP Tools](mcp-tools-reference.md)** - Complete tool reference
- **[Chat Modes](chat-modes.md)** - Usage patterns
- **[Examples](agent-examples.md)** - Real-world patterns

---

**Get started** → **[Cursor Integration](cursor-integration.md)**
