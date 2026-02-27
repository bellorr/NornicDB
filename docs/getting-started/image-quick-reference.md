# Docker Image Quick Reference

This page represents the full startup command matrix for common deployment profiles.

## Apple Silicon (M1/M2/M3)

```bash
# bge-m3 embedding model + heimdall
docker pull timothyswt/nornicdb-arm64-metal-bge-heimdall:latest
docker run -d -p 7474:7474 -p 7687:7687 -v nornicdb-data:/data \
  timothyswt/nornicdb-arm64-metal-bge-heimdall

# bge-m3 embedding model
docker pull timothyswt/nornicdb-arm64-metal-bge:latest
docker run -d -p 7474:7474 -p 7687:7687 -v nornicdb-data:/data \
  timothyswt/nornicdb-arm64-metal-bge

# BYOM
docker pull timothyswt/nornicdb-arm64-metal:latest
docker run -d -p 7474:7474 -p 7687:7687 -v nornicdb-data:/data \
  timothyswt/nornicdb-arm64-metal

# BYOM + no UI
docker pull timothyswt/nornicdb-arm64-metal-headless:latest
docker run -d -p 7474:7474 -p 7687:7687 -v nornicdb-data:/data \
  timothyswt/nornicdb-arm64-headless
```

## NVIDIA GPU (Windows/Linux)

```bash
# bge-m3 embedding model + heimdall
docker pull timothyswt/nornicdb-amd64-cuda-bge-heimdall:latest
docker run --gpus all -d -p 7474:7474 -p 7687:7687 -v nornicdb-data:/data \
  timothyswt/nornicdb-amd64-cuda-bge-heimdall

# bge-m3 embedding model
docker pull timothyswt/nornicdb-amd64-cuda-bge:latest
docker run --gpus all -d --gpus all -p 7474:7474 -p 7687:7687 -v nornicdb-data:/data \
  timothyswt/nornicdb-amd64-cuda-bge

# BYOM
docker pull timothyswt/nornicdb-amd64-cuda:latest
docker run --gpus all -d --gpus all -p 7474:7474 -p 7687:7687 -v nornicdb-data:/data \
  timothyswt/nornicdb-amd64-cuda
```

## CPU Only (Windows/Linux)

```bash
# BYOM
docker pull timothyswt/nornicdb-amd64-cpu:latest
docker run -d -p 7474:7474 -p 7687:7687 -v nornicdb-data:/data \
  timothyswt/nornicdb-amd64-cpu

# BYOM + no UI
docker pull timothyswt/nornicdb-amd64-cpu-headless:latest
docker run -d --gpus all -p 7474:7474 -p 7687:7687 -v nornicdb-data:/data \
  timothyswt/nornicdb-amd64-cpu-headless
```

## Vulkan (Linux)

```bash
docker pull timothyswt/nornicdb-amd64-vulkan:latest
docker run --gpus all -d -p 7474:7474 -p 7687:7687 -v nornicdb-data:/data \
  timothyswt/nornicdb-amd64-vulkan:latest

docker pull timothyswt/nornicdb-amd64-vulkan-bge:latest
docker run --gpus all -d -p 7474:7474 -p 7687:7687 -v nornicdb-data:/data \
  timothyswt/nornicdb-amd64-vulkan-bge:latest

docker pull timothyswt/nornicdb-amd64-vulkan-headless:latest
docker run --gpus all -d -p 7474:7474 -p 7687:7687 -v nornicdb-data:/data \
  timothyswt/nornicdb-amd64-vulkan-headless:latest
```

## Build Prerequisites

- Go 1.26 (for builds and cross-compilation)
- Docker (for image builds)
- curl (for model downloads)
- GNU make
- For local GGUF embeddings: working `llama.cpp` build (`scripts/build-llama.sh`)
- For CUDA builds on Linux/Windows: NVIDIA drivers + CUDA Toolkit (12.x recommended)
- For Vulkan builds on Linux: Vulkan runtime + GPU drivers
- On macOS (Apple Silicon): Docker + `--platform linux/arm64` for arm64 images
- Optional: `gh` CLI for GitHub release workflows

Model files:
- BGE: `models/bge-m3.gguf` (`make download-bge`)
- Qwen: `models/qwen3-0.6b-instruct.gguf` (`make download-qwen`)

## Common Make Flags

- Force no cache: `NO_CACHE=1`
- Set registry (default `timothyswt`): `REGISTRY=yourdockerid`
- Set tag version (default `latest`): `VERSION=1.0.6`
