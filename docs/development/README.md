# Development Guide

**Contributing to NornicDB development.**

## 📚 Documentation

- **[Development Setup](setup.md)** - Local development environment
- **[Code Style](code-style.md)** - Coding standards and conventions
- **[Testing](testing.md)** - Test guidelines and frameworks
- **[Documentation](documentation.md)** - Documentation standards
- **[Release Process](release-process.md)** - Release workflow

## 🚀 Quick Start

### Setup Development Environment

```bash
# Clone repository
git clone https://github.com/orneryd/nornicdb.git
cd nornicdb

# Install dependencies
go mod download

# Run tests
go test ./...

# Build
go build -o nornicdb ./cmd/nornicdb
```

[Complete setup guide →](setup.md)

### Code Style

- Follow Go conventions
- Use `gofmt` for formatting
- Write godoc comments
- Include examples

[Code style guide →](code-style.md)

### Testing

```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run specific package
go test ./pkg/storage/...
```

[Testing guide →](testing.md)

## 📖 Contributing

### Development Workflow

1. Fork the repository
2. Create feature branch
3. Write code and tests
4. Submit pull request
5. Code review
6. Merge

### Code Review Checklist

- [ ] Tests pass
- [ ] Code follows style guide
- [ ] Documentation updated
- [ ] No breaking changes (or documented)
- [ ] Performance impact considered

## 🆘 Getting Help

- **GitHub Issues** - Bug reports and feature requests
- **Discussions** - Questions and ideas
- **Discord** - Real-time chat

---

**Start contributing** → **[Development Setup](setup.md)**
