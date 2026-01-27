# HTTP/2 Implementation

**Date:** 2026-01-27  
**Status:** Implemented

## Overview

HTTP/2 support has been added to NornicDB's HTTP server. HTTP/2 is **always enabled** and is fully backwards compatible with HTTP/1.1 clients.

## Benefits

HTTP/2 provides several performance improvements:

1. **Multiplexing**: Multiple requests can be sent over a single TCP connection
2. **Header Compression**: Reduces overhead for repeated headers
3. **Server Push**: Enables proactive resource pushing (not currently used)
4. **Binary Protocol**: More efficient than HTTP/1.1's text-based protocol

**Expected Performance Improvement:**
- **10-20% latency reduction** for concurrent requests
- **Reduced connection overhead** for high-concurrency workloads
- **Better resource utilization** with connection multiplexing

## Implementation Details

### Configuration

HTTP/2 is automatically enabled with the following configuration:

```go
type Config struct {
    // HTTP/2 is always enabled (backwards compatible)
    HTTP2MaxConcurrentStreams uint32 // Default: 250 (matches Go's internal default)
}
```

### Modes

#### 1. HTTPS Mode (TLS)
When TLS certificates are configured:
- HTTP/2 is enabled via ALPN (Application-Layer Protocol Negotiation)
- Clients automatically negotiate HTTP/2 during TLS handshake
- Falls back to HTTP/1.1 for clients that don't support HTTP/2

#### 2. HTTP Mode (Cleartext)
When running without TLS:
- Uses **h2c** (HTTP/2 Cleartext) protocol
- Automatically detects HTTP/2 vs HTTP/1.1 clients
- Falls back to HTTP/1.1 for older clients

### Backwards Compatibility

**HTTP/2 is fully backwards compatible:**
- HTTP/1.1 clients continue to work without any changes
- No client-side configuration required
- Automatic protocol negotiation
- No breaking changes to existing APIs

## Usage

### Server Configuration

HTTP/2 is enabled by default. No configuration required:

```go
config := server.DefaultConfig()
config.HTTP2MaxConcurrentStreams = 500 // Optional: adjust concurrent streams (default: 250)

server, err := server.New(db, auth, config)
```

### Client Usage

#### HTTP/1.1 Clients (No Changes Required)

Existing HTTP/1.1 clients work without modification:

```bash
# curl (HTTP/1.1 by default)
curl http://localhost:7474/health

# HTTP/1.1 Go clients
resp, err := http.Get("http://localhost:7474/health")
```

#### HTTP/2 Clients (Automatic Upgrade)

Modern HTTP clients automatically use HTTP/2:

```bash
# curl with HTTP/2 (if supported)
curl --http2 http://localhost:7474/health

# Go http.Client automatically uses HTTP/2 when available
client := &http.Client{}
resp, err := client.Get("http://localhost:7474/health")
```

## Performance Impact

### Benchmark Results

From our profiling analysis:
- **Before HTTP/2:** 26,405 req/s, 0.57ms average latency
- **Expected with HTTP/2:** 10-20% latency reduction for concurrent requests
- **Connection overhead:** Reduced by multiplexing multiple requests per connection

### When HTTP/2 Helps Most

HTTP/2 provides the most benefit for:
1. **High concurrency workloads** - Multiple requests from same client
2. **Many small requests** - Header compression reduces overhead
3. **Latency-sensitive applications** - Reduced connection establishment overhead

### When HTTP/2 Has Minimal Impact

HTTP/2 has less impact for:
1. **Single request scenarios** - No multiplexing benefit
2. **Large payloads** - Header compression is less significant
3. **Low concurrency** - Connection overhead is already minimal

## Configuration Options

### MaxConcurrentStreams

Controls the maximum number of concurrent streams per HTTP/2 connection:

```go
config.HTTP2MaxConcurrentStreams = 250 // Default (matches Go's internal default)
```

**Default Value: 250**

The default of **250** matches Go's standard library `http2.Server` default:
- **Go's standard:** Matches `golang.org/x/net/http2` internal default (250)
- **Good balance:** Provides adequate concurrency without excessive memory usage
- **Security:** Reasonable protection against DoS attacks while allowing good performance
- **Typical workloads:** Sufficient for most use cases (250 concurrent requests per connection)

**Recommendations by Use Case:**

- **Default (most cases):** 250 streams
  - Matches Go's internal default
  - Good balance of performance and security
  - Adequate for typical API workloads

- **Lower memory usage:** 100 streams
  - Industry standard recommendation
  - Better security posture
  - Good for resource-constrained environments

- **High concurrency (50-200 clients):** 500-1000 streams
  - For scenarios with many concurrent clients
  - Each client can have multiple concurrent requests
  - Uses more memory per connection

- **Very high concurrency (200+ clients):** 1000+ streams
  - Only for specialized high-load scenarios
  - **Warning:** Values >1000 increase DoS attack risk
  - Monitor memory usage carefully

**Trade-offs:**
- **Higher values:** More concurrent requests per connection, but more memory usage and DoS risk
- **Lower values:** Less memory, better security, but may require more connections for high concurrency

**Memory Impact:**
Each stream requires memory for:
- Stream state tracking
- Flow control windows (connection-level and stream-level)
- Request/response buffers

**Example:** With 1000 streams and 100 connections, you could theoretically have 100,000 concurrent streams, which could consume significant memory if all are active.

**Security Consideration:**
Malicious clients can open connections and create the maximum number of streams, potentially exhausting server memory. The default of 100 provides a good balance between functionality and security.

## Testing

### Verify HTTP/2 is Enabled

Check server logs on startup:
```
🚀 HTTP/2 enabled (h2c cleartext mode, backwards compatible with HTTP/1.1)
```

Or for HTTPS:
```
🚀 HTTP/2 enabled (HTTPS mode)
```

### Test HTTP/2 Connection

```bash
# Test with curl (requires HTTP/2 support)
curl -v --http2 http://localhost:7474/health 2>&1 | grep -i "http/2"

# Expected output:
# < HTTP/2 200
```

### Benchmark Comparison

Run the HTTP write benchmark to measure impact:

```bash
go run testing/benchmarks/http_write_latency/main.go \
    -url http://localhost:7474 \
    -database nornic \
    -requests 5000 \
    -concurrency 20
```

Compare results with HTTP/1.1-only servers to see the improvement.

## Troubleshooting

### HTTP/2 Not Working

1. **Check server logs** - Should show "HTTP/2 enabled" message
2. **Verify client support** - Some older clients don't support HTTP/2
3. **Check network** - Some proxies/firewalls may block HTTP/2

### Performance Not Improved

1. **Low concurrency** - HTTP/2 benefits are most visible with multiple concurrent requests
2. **Single connection** - HTTP/2 multiplexing requires multiple requests on same connection
3. **Large payloads** - Header compression has less impact on large responses

### Compatibility Issues

If you encounter issues with specific clients:

1. **HTTP/2 is backwards compatible** - Clients should fall back to HTTP/1.1
2. **Check client logs** - May show protocol negotiation details
3. **Test with HTTP/1.1 explicitly** - Some clients allow forcing HTTP/1.1

## References

- [HTTP/2 Specification (RFC 7540)](https://tools.ietf.org/html/rfc7540)
- [Go HTTP/2 Package Documentation](https://pkg.go.dev/golang.org/x/net/http2)
- [h2c (HTTP/2 Cleartext) Documentation](https://pkg.go.dev/golang.org/x/net/http2/h2c)
