# Getting Started with NornicDB

Welcome to NornicDB! This guide will get you up and running in 5 minutes.

## 📋 Quick Navigation

- **[Installation Guide](installation.md)** - Install NornicDB on your system
- **[Quick Start](quick-start.md)** - Docker setup and first queries
- **[First Queries](first-queries.md)** - Learn basic Cypher queries
- **[Docker Deployment](docker-deployment.md)** - Production Docker setup

## 🎯 What You'll Learn

1. **Install NornicDB** - Get the database running locally
2. **Connect to the database** - Use Bolt protocol or HTTP API
3. **Run your first queries** - Create nodes, relationships, and search
4. **Understand the basics** - Graph concepts and Cypher syntax

## 🚀 Fastest Path

If you just want to try NornicDB right now:

```bash
# Pull and run Docker image
docker pull timothyswt/nornicdb-arm64-metal:latest
docker run -p 7474:7474 -p 7687:7687 timothyswt/nornicdb-arm64-metal:latest

# Open browser to http://localhost:7474
# Start querying!
```

See **[Quick Start](quick-start.md)** for details.

## 📚 Learning Path

### For Complete Beginners
1. Read [Installation Guide](installation.md)
2. Follow [Quick Start](quick-start.md)
3. Try [First Queries](first-queries.md)
4. Explore [User Guides](../user-guides/)

### For Neo4j Users
1. Check [Neo4j Migration Guide](../neo4j-migration/)
2. Review [Feature Parity](../neo4j-migration/feature-parity.md)
3. See [Cypher Compatibility](../neo4j-migration/cypher-compatibility.md)

### For AI Developers
1. Start with [Quick Start](quick-start.md)
2. Read [AI Integration Guide](../ai-agents/)
3. Try [Vector Search](../user-guides/vector-search.md)

## 🆘 Need Help?

- **Questions?** Check [User Guides](../user-guides/)
- **API Reference?** See [API Documentation](../api-reference/)
- **Issues?** Visit [Operations Guide](../operations/troubleshooting.md)

## ⏭️ Next Steps

After completing the getting started guides:

- **[User Guides](../user-guides/)** - Learn advanced features
- **[Features](../features/)** - Explore GPU acceleration, vector search
- **[API Reference](../api-reference/)** - Complete function documentation

---

**Ready to begin?** → **[Installation Guide](installation.md)**
