# llama.cpp Static Libraries

This directory contains static libraries and headers for llama.cpp, used by NornicDB's local embedding provider.

## Directory Structure

```
lib/llama/
├── llama.h                      # Main llama.cpp header
├── ggml.h                       # GGML tensor library header
├── libllama_darwin_arm64.a      # macOS Apple Silicon (with Metal)
├── libllama_darwin_amd64.a      # macOS Intel
├── libllama_linux_amd64.a       # Linux x86_64 (CPU only)
├── libllama_linux_amd64_cuda.a  # Linux x86_64 (with CUDA)
├── libllama_linux_arm64.a       # Linux ARM64
├── libllama_windows_amd64.a     # Windows x86_64 (with CUDA)
├── VERSION                      # llama.cpp version used
└── README.md                    # This file
```

## Building from Source

Run the build script from the nornicdb directory:

```bash
# Build for current platform
./scripts/build-llama.sh

# Build specific version
./scripts/build-llama.sh b4600
```

### Requirements

- CMake 3.14+
- C/C++ compiler (gcc, clang, MSVC)
- Git

### GPU Support

The script auto-detects GPU capabilities:

| Platform | GPU Backend | Detection |
|----------|-------------|-----------|
| macOS Apple Silicon | Metal | Automatic |
| Linux + NVIDIA | CUDA | Requires nvcc in PATH |
| Windows + NVIDIA | CUDA | Requires CUDA Toolkit |
| All platforms | CPU | Always available (AVX2/NEON) |

## Pre-built Libraries

For CI/CD, pre-built libraries can be downloaded from GitHub Releases or built via GitHub Actions.

### GitHub Actions Workflow

The workflow at `.github/workflows/build-llama.yml` builds libraries for all platforms:

```bash
# Trigger build manually
gh workflow run build-llama.yml
```

## Using with NornicDB

1. Place library files in this directory
2. Configure NornicDB:
   ```bash
   NORNICDB_EMBEDDING_PROVIDER=local
   NORNICDB_EMBEDDING_MODEL=bge-m3
   NORNICDB_MODELS_DIR=/data/models
   ```
3. Place your `.gguf` model in the models directory:
   ```bash
   cp bge-m3.Q4_K_M.gguf /data/models/bge-m3.gguf
   ```

## Placeholder Headers

The `llama.h` and `ggml.h` files in this directory are placeholders for development.
Running `./scripts/build-llama.sh` will replace them with actual headers from llama.cpp.

## Version Compatibility

- llama.cpp version: See `VERSION` file after building
- Recommended: b4535 or later (for stable embedding API)

## License

- llama.cpp: MIT License
- GGML: MIT License
- This build configuration: MIT License

Model files (`.gguf`) are NOT included and have their own licenses.
Users are responsible for complying with model licenses.
