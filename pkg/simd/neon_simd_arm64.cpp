// ARM64 NEON SIMD implementation for float32 vector operations
// Uses ARM NEON intrinsics for maximum performance on Apple Silicon and ARM64

#include <arm_neon.h>
#include <cmath>
#include <cstring>

// Dot product: sum(a[i] * b[i])
extern "C" float neon_dot_product(const float* a, const float* b, int len) {
    if (len == 0) return 0.0f;
    
    float32x4_t sum_vec = vdupq_n_f32(0.0f);
    int i = 0;
    
    // Process 4 elements at a time using NEON
    for (; i + 4 <= len; i += 4) {
        float32x4_t va = vld1q_f32(&a[i]);
        float32x4_t vb = vld1q_f32(&b[i]);
        float32x4_t prod = vmulq_f32(va, vb);
        sum_vec = vaddq_f32(sum_vec, prod);
    }
    
    // Horizontal sum of the vector
    float sum = vaddvq_f32(sum_vec);
    
    // Handle remaining elements
    for (; i < len; i++) {
        sum += a[i] * b[i];
    }
    
    return sum;
}

// Euclidean norm (L2 norm): sqrt(sum(v[i]^2))
extern "C" float neon_norm(const float* v, int len) {
    if (len == 0) return 0.0f;
    
    float32x4_t sum_vec = vdupq_n_f32(0.0f);
    int i = 0;
    
    // Process 4 elements at a time
    for (; i + 4 <= len; i += 4) {
        float32x4_t vv = vld1q_f32(&v[i]);
        float32x4_t squared = vmulq_f32(vv, vv);
        sum_vec = vaddq_f32(sum_vec, squared);
    }
    
    // Horizontal sum
    float sum = vaddvq_f32(sum_vec);
    
    // Handle remaining elements
    for (; i < len; i++) {
        sum += v[i] * v[i];
    }
    
    return std::sqrt(sum);
}

// Euclidean distance: sqrt(sum((a[i] - b[i])^2))
extern "C" float neon_distance(const float* a, const float* b, int len) {
    if (len == 0) return 0.0f;
    
    float32x4_t sum_vec = vdupq_n_f32(0.0f);
    int i = 0;
    
    // Process 4 elements at a time
    for (; i + 4 <= len; i += 4) {
        float32x4_t va = vld1q_f32(&a[i]);
        float32x4_t vb = vld1q_f32(&b[i]);
        float32x4_t diff = vsubq_f32(va, vb);
        float32x4_t squared = vmulq_f32(diff, diff);
        sum_vec = vaddq_f32(sum_vec, squared);
    }
    
    // Horizontal sum
    float sum = vaddvq_f32(sum_vec);
    
    // Handle remaining elements
    for (; i < len; i++) {
        float diff = a[i] - b[i];
        sum += diff * diff;
    }
    
    return std::sqrt(sum);
}

// Cosine similarity: dot(a,b) / (norm(a) * norm(b))
extern "C" float neon_cosine_similarity(const float* a, const float* b, int len) {
    if (len == 0) return 0.0f;
    
    float dot = neon_dot_product(a, b, len);
    float norm_a = neon_norm(a, len);
    float norm_b = neon_norm(b, len);
    
    if (norm_a == 0.0f || norm_b == 0.0f) {
        return 0.0f;
    }
    
    return dot / (norm_a * norm_b);
}

// Normalize vector in-place: v[i] = v[i] / norm(v)
extern "C" void neon_normalize_inplace(float* v, int len) {
    if (len == 0) return;
    
    float n = neon_norm(v, len);
    if (n == 0.0f) return;
    
    float inv_norm = 1.0f / n;
    float32x4_t inv_norm_vec = vdupq_n_f32(inv_norm);
    int i = 0;
    
    // Process 4 elements at a time
    for (; i + 4 <= len; i += 4) {
        float32x4_t vv = vld1q_f32(&v[i]);
        float32x4_t normalized = vmulq_f32(vv, inv_norm_vec);
        vst1q_f32(&v[i], normalized);
    }
    
    // Handle remaining elements
    for (; i < len; i++) {
        v[i] *= inv_norm;
    }
}

