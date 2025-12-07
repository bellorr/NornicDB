# Windows CUDA Implementation Plan

**Goal**: Enable Windows CUDA support in NornicDB using dynamic backend loading

**Status**: ✅ Infrastructure exists, just needs configuration updates

---

## Current State

NornicDB already has:
- ✅ llama.cpp integration (`pkg/localllm/llama.go`)
- ✅ Windows build target (line 50: `windows,amd64`)
- ✅ Static library linking pattern
- ✅ Build scripts in `scripts/build-llama.sh`
- ✅ Library directory `lib/llama/`

**What's Missing**: Dynamic backend loading for Windows CUDA

---

## The Simple Solution

### Before (Static Linking - Current)
```c
// Compile everything into one static library
#cgo windows,amd64 LDFLAGS: -L${SRCDIR}/../../lib/llama -lllama_windows_amd64 -lcudart -lcublas
```

**Problem**: Requires MSVC + MinGW ABI compatibility (doesn't work)

### After (Dynamic Backend Loading)
```c
// Link only base GGML library, load GPU backends at runtime
#cgo windows,amd64 LDFLAGS: -L${SRCDIR}/../../lib/llama -lllama_windows_amd64_base
```

**Solution**: CUDA compiled as separate DLL, loaded at runtime by GGML (works perfectly)

---

## Implementation Steps

### Step 1: Update Build Script (30 minutes)

**File**: `scripts/build-llama-windows.sh`

```bash
#!/bin/bash
# Build llama.cpp with dynamic backend loading for Windows

cmake -B build-windows \
    -DCMAKE_BUILD_TYPE=Release \
    -DCMAKE_SYSTEM_NAME=Windows \
    -DCMAKE_C_COMPILER=x86_64-w64-mingw32-gcc \
    -DCMAKE_CXX_COMPILER=x86_64-w64-mingw32-g++ \
    -DGGML_BACKEND_DL=ON \           # Enable dynamic loading
    -DGGML_BACKEND_SHARED=ON \       # Build backends as DLLs
    -DGGML_CUDA=OFF \                # Don't compile CUDA into base
    -DGGML_METAL=OFF \
    -DGGML_VULKAN=OFF \
    -DGGML_NATIVE=OFF \
    vendor/llama.cpp

cmake --build build-windows --config Release

# Output: 
# - libllama_windows_amd64_base.a  (base library, MinGW-compiled)
# - No CUDA code in base library!

# Copy to lib directory
cp build-windows/libllama.a lib/llama/libllama_windows_amd64_base.a
```

### Step 2: Build CUDA Backend DLL (separate, with MSVC)

**File**: `scripts/build-cuda-backend-windows.bat`

```batch
@echo off
REM Build CUDA backend DLL with MSVC (Windows native)

cmake -B build-cuda-windows ^
    -G "Visual Studio 17 2022" ^
    -A x64 ^
    -DCMAKE_BUILD_TYPE=Release ^
    -DGGML_BACKEND_DL=ON ^
    -DGGML_BACKEND_SHARED=ON ^
    -DGGML_CUDA=ON ^
    -DCMAKE_CUDA_ARCHITECTURES="60;70;75;80;86;89;90" ^
    vendor/llama.cpp/ggml

cmake --build build-cuda-windows --config Release

REM Output:
REM - ggml-cuda.dll (MSVC-compiled, contains all CUDA code)
REM - This DLL is loaded at runtime, no ABI conflicts!

copy build-cuda-windows\bin\Release\ggml-cuda.dll lib\llama\
```

### Step 3: Update Go CGO Flags (5 minutes)

**File**: `pkg/localllm/llama.go`

```go
// Line 50: Change from static to dynamic
// OLD:
// #cgo windows,amd64 LDFLAGS: -L${SRCDIR}/../../lib/llama -lllama_windows_amd64 -lcudart -lcublas

// NEW:
#cgo windows,amd64 LDFLAGS: -L${SRCDIR}/../../lib/llama -lllama_windows_amd64_base
```

**That's it!** No CUDA libraries linked at compile time.

### Step 4: Add Backend Loading Call (10 minutes)

**File**: `pkg/localllm/llama.go`

Add this to the C section (around line 56):

```c
// Load GPU backends from directory
void load_gpu_backends(const char* path) {
    #ifdef _WIN32
        // On Windows, load backends from executable directory
        ggml_backend_load_all_from_path(path);
    #endif
}
```

Update `init_backend()` function (around line 59):

```c
void init_backend() {
    if (!initialized) {
        llama_backend_init();
        
        #ifdef _WIN32
            // Load GPU backends from lib directory
            load_gpu_backends("lib/llama");
        #endif
        
        initialized = 1;
    }
}
```

### Step 5: Update Dockerfile (10 minutes)

**File**: `docker/Dockerfile.amd64-cuda`

```dockerfile
FROM nvidia/cuda:12.3-runtime-ubuntu22.04

# Copy NornicDB binary
COPY bin/nornicdb /usr/local/bin/

# Copy CUDA backend DLL
COPY lib/llama/ggml-cuda.dll /usr/local/lib/ggml/
COPY lib/llama/cudart64_12.dll /usr/local/lib/ggml/

# Set library path
ENV PATH=/usr/local/lib/ggml:$PATH

ENTRYPOINT ["/usr/local/bin/nornicdb"]
```

### Step 6: Update Documentation (20 minutes)

**File**: `docs/getting-started/windows-cuda.md`

```markdown
# Windows CUDA Setup

## Prerequisites
- Windows 10/11
- NVIDIA GPU with CUDA support
- CUDA Toolkit 12.x installed

## Installation

1. Download NornicDB Windows release
2. Download CUDA backend DLL package
3. Extract both to same directory:
   ```
   nornicdb/
   ├── nornicdb.exe
   └── lib/
       └── llama/
           ├── ggml-cuda.dll
           └── cudart64_12.dll
   ```

## Verify GPU Detection

```bash
nornicdb.exe --check-gpu

# Output:
# GPU: NVIDIA GeForce RTX 4090 (24 GB)
# Backend: CUDA 12.3
# Status: Ready
```

## That's It!

NornicDB will automatically use GPU for embeddings.
```

---

## Testing Plan

### Test 1: CPU Fallback (No CUDA DLL present)
```bash
# Remove CUDA DLL
rm lib/llama/ggml-cuda.dll

# Run NornicDB - should fall back to CPU
nornicdb.exe

# Expected: "GPU backend not found, using CPU"
```

### Test 2: CUDA Acceleration (CUDA DLL present)
```bash
# Add CUDA DLL back
# Run NornicDB
nornicdb.exe

# Expected: "GPU: NVIDIA ... Backend: CUDA"
```

### Test 3: Performance Validation
```bash
# Benchmark embeddings
nornicdb.exe benchmark --embeddings --count 1000

# Expected: 10-50x speedup vs CPU
```

---

## File Changes Summary

| File | Change | Lines |
|------|--------|-------|
| `scripts/build-llama-windows.sh` | Add dynamic backend flags | 20 |
| `scripts/build-cuda-backend-windows.bat` | New CUDA backend builder | 25 |
| `pkg/localllm/llama.go` | Update LDFLAGS + add backend loading | 15 |
| `docker/Dockerfile.amd64-cuda` | Copy backend DLLs | 5 |
| `docs/getting-started/windows-cuda.md` | New user guide | 50 |
| **TOTAL** | | **~115 lines** |

---

## Effort Estimation

| Task | Time | Owner |
|------|------|-------|
| Update build scripts | 1h | DevOps |
| Build CUDA backend DLL | 1h | DevOps |
| Update Go code | 30min | Dev |
| Update Docker files | 30min | DevOps |
| Write documentation | 1h | Tech Writer |
| Testing (CPU + GPU) | 2h | QA |
| **TOTAL** | **6 hours** | |

**vs. Original Estimate**: 60 hours → **6 hours** (90% reduction!)

---

## Why This Is So Much Simpler

### Original Plan (Separate Module)
- New repository
- New build system
- New tests
- New documentation
- Integration work
- Maintenance burden

### This Plan (Update Existing)
- ✅ Use existing infrastructure
- ✅ Minimal code changes
- ✅ Existing tests still work
- ✅ Update docs in place
- ✅ No new dependencies
- ✅ Single codebase

---

## Comparison Matrix

| Aspect | Separate Module | Update Existing |
|--------|----------------|-----------------|
| **Effort** | 60 hours | 6 hours |
| **Code Changes** | 2000+ lines | 115 lines |
| **New Files** | 20+ | 2 |
| **Maintenance** | New module | None |
| **Reusability** | ✅ High | ❌ NornicDB only |
| **Time to Ship** | 2-3 weeks | 1 day |
| **Risk** | Medium | Low |

**For NornicDB's needs**: Update existing is **clearly superior**

**For Go ecosystem**: Separate module is better long-term

---

## Decision

### Option A: Ship Now (Update Existing)
**Timeline**: 1 day  
**Benefit**: NornicDB gets Windows CUDA immediately  
**Cost**: Not reusable by other projects  

### Option B: Build Ecosystem (Separate Module)
**Timeline**: 2-3 weeks  
**Benefit**: Go community benefits, cleaner separation  
**Cost**: Longer time to ship  

### Recommendation: **Do Both, Sequentially**

1. **Phase 1 (This Week)**: Update NornicDB's existing code
   - Gets Windows CUDA working immediately
   - Validates the approach works
   - Ships to users fast

2. **Phase 2 (Next Month)**: Extract to separate module
   - Clean up the implementation
   - Make it reusable
   - Open-source for Go community
   - NornicDB switches to module

**Best of both worlds**: Ship fast, then make it reusable.

---

## Next Actions

### Immediate (Today)
1. ✅ Validate this plan (Done - you're reading it)
2. 🔨 Create Windows build scripts
3. 🏗️ Build CUDA backend DLL
4. ✍️ Update Go code
5. 🧪 Test on Windows VM

### This Week
1. 📦 Package Windows release
2. 📝 Update documentation
3. 🚀 Ship to users
4. 📊 Gather feedback

### Next Month
1. 🔄 Extract to `go-ggml-runtime` module
2. 🌟 Open-source
3. 📣 Announce to Go community
4. 🔁 NornicDB migrates to module

---

## Conclusion

**We don't need a separate module to solve NornicDB's Windows CUDA problem.**

The infrastructure is already there. We just need to:
1. Build llama.cpp with `GGML_BACKEND_DL=ON`
2. Add one function call: `ggml_backend_load_all_from_path()`
3. Ship CUDA backend DLL alongside binary

**6 hours of work vs 60 hours.**

**Let's ship it!** 🚀
