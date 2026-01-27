# Single Request Performance Benchmark

**Date:** 2026-01-27  
**Test:** HTTP Write Performance with 1 Concurrent Request (Best Case)  
**Configuration:** HTTP/2 + Basic Auth, Single Sequential Request

## Test Configuration

- **Requests:** 50,000
- **Concurrency:** 1 (sequential, no concurrency)
- **Database:** nornic
- **Warmup:** 10 requests
- **HTTP/2:** Enabled (h2c cleartext mode)
- **Authentication:** JWT Bearer token (admin:password)
- **Optimizations:** Executor caching + Search service reuse enabled
- **Purpose:** Measure best-case latency without concurrency overhead

## Results

### Performance Metrics

| Metric | Value |
|--------|-------|
| **Total Duration** | 6.77 seconds |
| **Throughput** | **7,382.69 req/s** |
| **Success Rate** | 100% (50,000/50,000) |
| **Min Latency** | **96.5µs (0.10 ms)** |
| **P50 (median)** | **132.5µs (0.13 ms)** |
| **P95** | **169.3µs (0.17 ms)** |
| **P99** | **216.8µs (0.22 ms)** |
| **P99.9** | **365.2µs (0.37 ms)** |
| **Max Latency** | **1.98 ms** |
| **Average Latency** | **135.3µs (0.14 ms)** |

## Analysis

### Best-Case Performance

With **1 concurrent request**, we achieve:
- **Sub-millisecond latency** for 99% of requests (P99: 0.22ms)
- **Ultra-low average latency**: 135.3µs (0.14ms)
- **Fastest possible request**: 96.5µs (0.10ms minimum)
- **Consistent performance**: Very tight latency distribution
- **High throughput**: 7,382.69 req/s even with sequential requests

### Latency Distribution

The latency distribution shows excellent consistency:
- **P50-P95 spread**: Only 36.8µs (132.5µs → 169.3µs)
- **P95-P99 spread**: Only 47.5µs (169.3µs → 216.8µs)
- **99.9th percentile**: Still under 0.4ms (365.2µs)
- **Maximum**: 1.98ms (likely due to occasional GC or system activity)

### Comparison: 1 vs 99 Concurrent Requests

| Metric | 1 Concurrent | 99 Concurrent (Optimized) | Difference | Speedup |
|--------|--------------|---------------------------|-----------|--------|
| **Throughput** | 7,382.69 req/s | **37,615 req/s** | **+409%** | **5.1x** |
| **Average Latency** | 0.14 ms | **2.63 ms** | +1,779% | **18.8x slower** |
| **P50 Latency** | 0.13 ms | **2.47 ms** | +1,800% | **18.6x slower** |
| **P99 Latency** | 0.22 ms | **7.30 ms** | +3,218% | **33.2x slower** |
| **P99.9 Latency** | 0.37 ms | **22.38 ms** | +5,946% | **60.5x slower** |

### Key Insights

1. **Sequential Processing is Fast**
   - Single requests achieve sub-millisecond latency (P99: 0.22ms)
   - **Fastest possible request: 96.5µs (0.10ms)**
   - No concurrency overhead or contention
   - Ideal for low-latency, single-user scenarios

2. **Concurrency Increases Throughput**
   - 99 concurrent requests: **5.1x higher throughput** (37,615 req/s)
   - Trade-off: Higher latency due to queuing and resource contention
   - Better for high-throughput, multi-user scenarios
   - **Memory optimizations improve both throughput and latency**

3. **Latency vs Throughput Trade-off**
   - **1 concurrent**: Best latency (0.14ms avg), lower throughput (7.4K req/s)
   - **99 concurrent (optimized)**: Higher latency (2.63ms avg), best throughput (37.6K req/s)
   - Choose based on use case requirements

4. **Consistent Performance**
   - Even at P99.9, single requests stay under 0.4ms (365.2µs)
   - Very predictable latency for sequential workloads
   - Minimal variance in response times
   - **Memory optimizations maintain consistency**

## Use Cases

### Single Request (1 Concurrent) - Best For:
- ✅ **Low-latency APIs** - When response time is critical
- ✅ **Single-user applications** - Desktop apps, CLI tools
- ✅ **Real-time systems** - Where sub-millisecond latency matters
- ✅ **Interactive queries** - User-facing applications
- ✅ **Testing baseline** - Understanding best-case performance

### High Concurrency (99 Concurrent) - Best For:
- ✅ **High-throughput APIs** - When total requests/sec matters
- ✅ **Multi-user applications** - Web services, microservices
- ✅ **Batch processing** - Parallel data ingestion
- ✅ **Load testing** - Understanding system limits
- ✅ **Production workloads** - Real-world multi-user scenarios

## Conclusion

**Single request performance demonstrates:**
- ✅ **Sub-millisecond latency** for 99% of requests (P99: 0.22ms)
- ✅ **Fastest possible request: 96.5µs (0.10ms)**
- ✅ **Ultra-consistent** response times (low variance)
- ✅ **Excellent baseline** for understanding system overhead
- ✅ **7,382.69 req/s** throughput even with sequential processing
- ✅ **Memory optimizations** maintain performance (1.4 KB/request overhead)

**The system achieves excellent single-request latency (0.14ms average) while maintaining high throughput under concurrency (37.6K req/s with 99 concurrent requests).**

This shows NornicDB can handle both:
- **Low-latency use cases** (single requests: <0.4ms P99.9, 96.5µs minimum)
- **High-throughput use cases** (concurrent requests: 37.6K req/s with optimizations)
