# Windows CUDA Setup

Enable GPU acceleration on Windows using NVIDIA CUDA.

## Prerequisites

- **Windows 10/11** (64-bit)
- **NVIDIA GPU** with CUDA Compute Capability 6.0 or higher
- **CUDA Toolkit 12.x** ([Download](https://developer.nvidia.com/cuda-downloads))

## Quick Start

### 1. Download NornicDB

Download the latest Windows release:
```
nornicdb-windows-amd64-cuda.zip
```

### 2. Extract Files

Extract to a directory of your choice:
```
C:\nornicdb\
├── nornicdb.exe
└── lib\
    └── llama\
        ├── ggml-cuda.dll      (CUDA backend)
        ├── cudart64_12.dll    (CUDA runtime)
        └── cublas64_12.dll    (cuBLAS)
```

### 3. Verify GPU Detection

Open PowerShell or Command Prompt:
```powershell
cd C:\nornicdb
.\nornicdb.exe --version

# Should show:
# NornicDB v1.x.x
# GPU: NVIDIA GeForce RTX 4090 (24 GB)
# Backend: CUDA 12.3
```

### 4. Run NornicDB

```powershell
.\nornicdb.exe
```

**That's it!** NornicDB will automatically use your GPU for embeddings and inference.

---

## Verification

### Check GPU Utilization

While NornicDB is running, open another terminal:

```powershell
# Watch GPU usage in real-time
nvidia-smi -l 1
```

You should see:
- GPU utilization increasing during queries
- Memory usage showing loaded models
- Temperature rising during inference

### Benchmark Performance

```powershell
# Generate 1000 embeddings
.\nornicdb.exe benchmark --embeddings --count 1000

# Expected output:
# Embeddings: 1000/1000
# Time: 2.3s
# Rate: 434 embeddings/sec
# GPU Utilization: 95%
```

Compare with CPU-only mode (move CUDA DLL temporarily):
```powershell
# Temporarily disable GPU
rename lib\llama\ggml-cuda.dll ggml-cuda.dll.bak

# Run benchmark again
.\nornicdb.exe benchmark --embeddings --count 1000

# Expected output:
# Embeddings: 1000/1000
# Time: 45.8s
# Rate: 21 embeddings/sec
# GPU Utilization: 0%

# Restore GPU
rename lib\llama\ggml-cuda.dll.bak ggml-cuda.dll
```

**Expected Speedup**: 10-50x faster with GPU vs CPU (depends on model and hardware)

---

## Troubleshooting

### GPU Not Detected

**Symptom**: NornicDB reports "GPU backend not found, using CPU"

**Solutions**:

1. **Verify CUDA DLLs are present**:
   ```powershell
   dir lib\llama\*.dll
   
   # Should show:
   # ggml-cuda.dll
   # cudart64_12.dll
   # cublas64_12.dll
   ```

2. **Check CUDA Toolkit version**:
   ```powershell
   nvcc --version
   
   # Should show CUDA 12.x
   ```

3. **Verify GPU is CUDA-capable**:
   ```powershell
   nvidia-smi
   
   # Should list your GPU
   ```

4. **Check PATH environment variable**:
   ```powershell
   echo %PATH%
   
   # Should include: C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA\v12.x\bin
   ```

### DLL Load Error

**Symptom**: "The code execution cannot proceed because ggml-cuda.dll was not found"

**Solution**: Ensure DLLs are in the correct location:
```powershell
# DLLs must be in lib\llama\ relative to nornicdb.exe
# Or add to PATH:
set PATH=%PATH%;C:\nornicdb\lib\llama
```

### Out of Memory Error

**Symptom**: "CUDA out of memory"

**Solutions**:

1. **Use a smaller model**:
   - Try quantized models (Q4_K_M, Q5_K_M instead of F16)
   - Use fewer GPU layers

2. **Close other GPU applications**:
   - Chrome/Edge (hardware acceleration)
   - Games
   - Other ML applications

3. **Check available VRAM**:
   ```powershell
   nvidia-smi --query-gpu=memory.free,memory.total --format=csv
   ```

### Slow Performance

**Symptom**: GPU is detected but performance is slower than expected

**Solutions**:

1. **Verify GPU is actually being used**:
   ```powershell
   nvidia-smi -l 1
   # GPU utilization should be >80% during queries
   ```

2. **Check Power Mode**:
   - NVIDIA Control Panel → Manage 3D Settings → Power Management Mode → "Prefer Maximum Performance"

3. **Update GPU Drivers**:
   - Download latest drivers from [NVIDIA](https://www.nvidia.com/download/index.aspx)

4. **Check thermal throttling**:
   ```powershell
   nvidia-smi --query-gpu=temperature.gpu,power.draw --format=csv
   # Temperature should be <85°C
   ```

---

## Configuration

### Custom Model Path

Set environment variable to use models from a custom location:

```powershell
set NORNICDB_MODEL_DIR=D:\models
.\nornicdb.exe
```

### GPU Layer Configuration

Control how many model layers run on GPU:

```yaml
# nornicdb.yaml
embed:
  model: bge-m3-q8_0.gguf
  gpu_layers: -1  # -1 = all layers (default)
                  #  0 = CPU only
                  # 20 = 20 layers on GPU, rest on CPU
```

---

## Advanced: Building from Source

### Prerequisites

- **Visual Studio 2022** (Community Edition or higher)
- **CUDA Toolkit 12.x**
- **Go 1.21+**
- **CMake 3.14+**

### Build Steps

#### 1. Build Base Library (MinGW)

```bash
# From macOS/Linux with MinGW cross-compiler
brew install mingw-w64  # macOS
# or
apt install mingw-w64   # Linux

cd NornicDB
./scripts/build-llama-windows.sh
```

#### 2. Build CUDA Backend (on Windows)

```batch
REM Open "Developer Command Prompt for VS 2022"
cd NornicDB
scripts\build-cuda-backend-windows.bat
```

#### 3. Build NornicDB

```powershell
# On Windows
cd NornicDB
$env:CGO_ENABLED=1
go build -o nornicdb.exe ./cmd/nornicdb
```

#### 4. Test

```powershell
# Copy CUDA DLLs
mkdir lib\llama
copy build-cuda-windows\bin\Release\ggml-cuda.dll lib\llama\
copy "%CUDA_PATH%\bin\cudart64_12.dll" lib\llama\
copy "%CUDA_PATH%\bin\cublas64_12.dll" lib\llama\

# Run
.\nornicdb.exe
```

---

## Architecture

### Dynamic Backend Loading

NornicDB uses a two-part architecture on Windows:

```
┌─────────────────────────────────────────────┐
│ nornicdb.exe (MinGW-compiled)              │
│   ↓ Static link                            │
│ libllama_windows_amd64.a                   │
│   ↓ Dynamic load at runtime                │
│ lib/llama/ggml-cuda.dll (MSVC-compiled)    │
│   ↓ Links to                               │
│ cudart64_12.dll, cublas64_12.dll           │
└─────────────────────────────────────────────┘
```

**Benefits**:
- ✅ No ABI compatibility issues (MinGW + MSVC work together)
- ✅ Automatic GPU detection and fallback to CPU
- ✅ Easy distribution (just include DLLs)
- ✅ Same approach as Ollama (proven at scale)

### Why This Works

1. **Base library** (MinGW) has no CUDA code
2. **CUDA backend** (MSVC) is loaded at runtime via standard C API
3. **No C++ ABI crossing** between MinGW and MSVC
4. **Universal CRT** provides C-level compatibility

---

## FAQ

### Q: Can I use my existing CUDA installation?

**A:** Yes! NornicDB will use your system's CUDA libraries. The bundled DLLs are for convenience.

### Q: What GPU architectures are supported?

**A:** CUDA Compute Capability 6.0+
- Pascal (GTX 10xx, Tesla P series)
- Volta (Tesla V100)
- Turing (RTX 20xx, GTX 16xx)
- Ampere (RTX 30xx, A series)
- Ada Lovelace (RTX 40xx)
- Hopper (H100)

### Q: Can I use ROCm (AMD GPUs) on Windows?

**A:** Not currently. ROCm is Linux-only. AMD GPU support on Windows would require Vulkan backend (coming soon).

### Q: Does this work with WSL2?

**A:** Yes! WSL2 supports CUDA. Use the Linux build and follow the [Linux CUDA guide](linux-cuda.md).

### Q: What about older CUDA versions?

**A:** CUDA 11.x may work, but 12.x is recommended for best performance and compatibility.

---

## Performance Tips

### 1. Use Quantized Models

```
Q4_K_M:  4-bit, good quality, 4x smaller
Q5_K_M:  5-bit, better quality, 3x smaller
Q8_0:    8-bit, near-original quality, 2x smaller
F16:     Full precision (largest, slowest)
```

### 2. Optimize Batch Sizes

```yaml
# nornicdb.yaml
embed:
  batch_size: 512  # Larger = better GPU utilization
                   # But requires more VRAM
```

### 3. Monitor Performance

```powershell
# Real-time GPU monitoring
nvidia-smi dmon -s ucm

# Log to file
nvidia-smi --query-gpu=timestamp,name,utilization.gpu,memory.used,memory.total --format=csv -l 1 > gpu.log
```

---

## Support

### Getting Help

- **GitHub Issues**: [NornicDB Issues](https://github.com/nornicdb/nornicdb/issues)
- **Discord**: [NornicDB Community](https://discord.gg/nornicdb)
- **Email**: support@nornicdb.io

### Reporting Bugs

Include this information:
```powershell
# System info
systeminfo | findstr /C:"OS Name" /C:"OS Version"

# GPU info
nvidia-smi --query-gpu=name,driver_version,memory.total --format=csv

# CUDA info
nvcc --version

# NornicDB version
.\nornicdb.exe --version
```

---

**Ready to go!** 🚀 Your Windows machine is now GPU-accelerated for NornicDB.
