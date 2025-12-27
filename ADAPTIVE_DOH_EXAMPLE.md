# Adaptive DoH Example Configuration

This example shows how to use adaptive DoH feature in mosdns.

**Note**: Adaptive DoH is now **enabled by default** for all HTTPS upstreams. No configuration changes are needed to use this feature.

## Overview

The adaptive DoH feature allows mosdns to automatically select between DoH and DoH3 (HTTP/3) protocols based on performance metrics. The system:

1. Initially tries both protocols alternately
2. Tracks latency and success rate for each protocol
3. After collecting enough samples (default: 10 queries), evaluates which protocol performs better
4. Switches to preferred protocol if it meets 80% performance threshold
5. Continuously monitors and switches if preferred protocol degrades

## Configuration

**Adaptive DoH is enabled by default for all HTTPS upstreams**. You don't need to explicitly set it.

```yaml
plugins:
  - tag: forward_plugin
    type: forward
    args:
      upstreams:
        - tag: cloudflare_doh
          addr: https://cloudflare-dns.com/dns-query
          # adaptive_doh: true  # Optional - enabled by default for HTTPS
          # Other options:
          # idle_timeout: 30
          # insecure_skip_verify: false
          # bootstrap: "8.8.8.8"
          # bootstrap_version: 0
```

### Disable Adaptive DoH

If you want to use a specific protocol instead of adaptive mode:

```yaml
upstreams:
  - tag: cloudflare_doh
    addr: https://cloudflare-dns.com/dns-query
    enable_http3: true  # Force DoH3 only (disables adaptive mode)
    # OR
    adaptive_doh: false  # Use DoH only (explicitly disable adaptive mode)
```

## How it Works

### Trial Phase (First N queries)
- System alternates between DoH and DoH3 for each query
- Collects performance metrics (latency, success/failure rate)
- Default trial count: 10 queries

### Evaluation Phase
- Compares average latency of both protocols
- Checks if DoH3 is available (failure rate < 50%)
- If DoH3 is at least 80% faster than DoH, it becomes preferred
- Otherwise, DoH remains preferred

### Steady State
- Uses preferred protocol for most queries
- Periodically validates preferred protocol is still performing well
- Automatically switches back if preferred protocol degrades
- Falls back to alternative if preferred fails

## Configuration Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `adaptive_doh` | bool | `true` for HTTPS, `false` otherwise | Enable adaptive protocol selection |
| `enable_http3` | bool | false | Force DoH3 only (overrides adaptive mode) |
| `idle_timeout` | int | 30 | Connection idle timeout in seconds |

## Monitoring

The adaptive DoH logs detailed information about:
- Protocol used for each query (debug level)
- Query success/failure with latency (debug level)
- Initial protocol evaluation results
- Protocol switches due to performance changes
- Failure rate thresholds
- Detailed statistics during evaluation

### Log Levels

**DEBUG** (enable for detailed per-query info):
```
DEBUG using protocol for query  protocol=doh  preferred=doh3
DEBUG query succeeded  protocol=doh  latency=45ms
DEBUG using fallback protocol  fallback=doh  preferred=doh3
```

**INFO** (default):
```
INFO protocol evaluation  doh_latency=85.5  doh_success=95  doh_failed=5  doh3_latency=42.3  doh3_success=98  doh3_failed=2  preference_threshold=0.8
INFO switched preferred protocol to DoH3 (faster)  doh_latency=85.5  doh3_latency=42.3  improvement=50.5
```

**WARN** (important events):
```
WARN switching preferred protocol due to failures  upstream=https://cloudflare-dns.com/dns-query  from=doh3  to=doh  old_success_rate=0.45  new_success_rate=0.95  old_failed=12  old_total=20  new_failed=1  new_total=20
WARN query failed  upstream=https://cloudflare-dns.com/dns-query  protocol=doh3  latency=100ms  error="context deadline exceeded"
```

### Enabling Debug Logs

To see per-query protocol selection and performance:

```yaml
plugins:
  - tag: forward_plugin
    type: forward
    args:
      upstreams:
        - tag: cloudflare
          addr: https://cloudflare-dns.com/dns-query
```

Then run with debug logging:
```bash
mosdns -c config.yaml -v debug
```

### Example Log Output

**Normal operation:**
```
INFO  upstream=https://cloudflare-dns.com/dns-query  protocol evaluation  doh_latency=85.5  doh_success=95  doh_failed=5  doh3_latency=42.3  doh3_success=98  doh3_failed=2  preference_threshold=0.8
INFO  upstream=https://cloudflare-dns.com/dns-query  switched preferred protocol to DoH3 (faster)  doh_latency=85.5  doh3_latency=42.3  improvement=50.5
```

**When DoH3 is failing:**
```
WARN  upstream=https://cloudflare-dns.com/dns-query  query failed  protocol=doh3  latency=150ms  error="http3: parsing frame failed: received a stateless reset"
WARN  upstream=https://cloudflare-dns.com/dns-query  switching preferred protocol due to failures  from=doh3  to=doh  old_success_rate=0.35  new_success_rate=0.95  old_failed=15  old_total=20  new_failed=1  new_total=20
```

**DoH3 unavailable:**
```
INFO  upstream=https://cloudflare-dns.com/dns-query  DoH3 failure rate too high, using DoH  failure_rate=0.68  doh3_failed=14  doh3_total=20
```

## When to Use

**Adaptive DoH is now enabled by default for HTTPS upstreams**, so you're already using it unless explicitly disabled.

Consider disabling adaptive DoH when:
- You need deterministic protocol selection for testing or debugging
- Network has strict firewall rules that consistently block one protocol
- You've benchmarked and know one protocol always works better for your network

Use fixed protocols (enable_http3 or adaptive_doh: false) when:
- Troubleshooting connectivity issues
- Network conditions are stable and predictable
- You need consistent behavior for performance testing

## Performance Considerations

- Initial trial phase has slightly higher latency as both protocols are tested
- Steady state performance is optimized based on actual network conditions
- Minimal overhead for ongoing protocol switching
- Connections are reused within each protocol pool

## Comparison with Fixed Protocol

| Feature | Fixed DoH | Fixed DoH3 | Adaptive DoH (Default) |
|---------|-----------|------------|----------------------|
| Initial setup | Simple | Simple | Simple (default for HTTPS) |
| Network adaptability | None | None | Automatic |
| Best performance | Maybe | Maybe | Automatic |
| Failure handling | Manual | Manual | Automatic |
| Protocol switching | No | No | Yes |
| Configuration | `enable_http3: false` | `enable_http3: true` | Default (no config needed) |
