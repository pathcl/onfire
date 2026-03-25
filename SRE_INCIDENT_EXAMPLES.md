# SRE Incident Patterns - Practical Examples & Remediation
## Real-World Scenarios for Firecracker MicroVM Training

This document provides concrete, actionable examples of infrastructure failures that trainees will encounter in onfire scenarios, with diagnostic techniques and remediation steps.

---

## SCENARIO 1: Disk Space Crisis (Easy)

### Setup
```yaml
scenario:
  title: "The Disk That Ate Everything"
  difficulty: easy
faults:
  - type: disk
    path: /
    at: 0s
```

### What Happens to the Trainee
1. **Initial symptom**: Application returns 502 Bad Gateway errors; `nginx` cannot write to log files
2. **Warning signs**:
   - HTTP requests hang and timeout
   - Deployment pipeline reports "no space left on device"
   - New sessions cannot be created (temp files fail)

### Diagnostic Steps
```bash
# Check overall disk usage
$ df -h
Filesystem      Size  Used Avail Use% Mounted on
/dev/vda1       1.0G  967M   33M  97% /

# Find large files (sorted by size)
$ du -sh /* 2>/dev/null | sort -rh | head -10
500M    /.onfire-diskfill
200M    /var/log
100M    /tmp

# Check inode usage
$ df -i
Filesystem     Inodes IUsed IFree IUse% Mounted on
/dev/vda1     130560 20145 110415   16% /

# List files by modification time (recent changes)
$ find / -type f -printf '%T@ %s %p\n' 2>/dev/null | sort -rn | head -5
```

### Root Cause Discovery
```bash
# The .onfire-diskfill file is the culprit
$ ls -lah / | grep onfire-diskfill
-rw-r--r-- 1 root root 500M /.onfire-diskfill

# This is the injected fault—a hidden file filling disk
```

### Remediation
```bash
# Option 1: Remove the fault file
$ rm /.onfire-diskfill
$ df -h
# Disk usage drops to 20%

# Option 2: Clean other low-hanging fruit
$ rm -rf /var/log/*.log*
$ find /tmp -type f -delete
$ apt-get clean
$ journalctl --vacuum=10M

# Verify nginx recovers
$ systemctl status nginx
$ curl http://localhost/healthz
200 OK
```

### Scoring
- **Base score**: 100 points
- **Time penalty**: -0.05 points/second (5 minutes = -15 points)
- **Perfect window**: 5 minutes
- **Perfect score**: Recover disk <80% within 5 minutes = 100 points

### Teaching Points
- Disk usage grows subtly; often first symptom is "can't write" not "disk full"
- Hidden files (.filename) are easy to miss without `ls -la`
- Applications fail *before* you notice the high disk usage
- Cascading effects: nginx can't log, then can't create tmp files, then crashes
- `du -sh /*` finds the culprit; `du -sh` alone might miss shadow allocations

---

## SCENARIO 2: Cascading Connection Pool Exhaustion (Medium)

### Setup
```yaml
scenario:
  title: "Connection Pool Meltdown"
  difficulty: medium
architecture: microservices
environment:
  tiers:
    - name: api
      count: 2
      services: [nginx, app-server]
    - name: db
      count: 1
      services: [postgresql]

faults:
  # Stage 1: DB CPU spike (overload database)
  - type: cpu
    target: {tier: db}
    params: {load: "90%"}
    at: 0s
    duration: 3m

  # Stage 2: Network latency (slow connection establishment)
  - type: network
    target: {tier: api}
    params: {action: delay, latency: 300ms, jitter: 50ms}
    at: 1m
    duration: 5m

  # Stage 3: Memory leak on api-0 (simulates connection object leak)
  - type: memory
    target: {vm: api-0}
    params: {rate: "30mb/min", ceiling: "85%"}
    at: 2m
```

### What Happens
**T+0s**: DB CPU spikes to 90%
- Database query processing slows down
- Existing connections still work but new ones take longer to open
- First symptom in logs: database query latency increases

**T+1m**: Network latency injected on API tier
- Establishing connections to DB now takes 300+ms instead of 10ms
- Connection pool exhaustion begins (slow opening, fast timeout expiry)
- Connection timeout config (default 30s) expires before pool recovers
- API tier cannot open new connections; reuses old stale ones
- `curl /api/endpoint` starts hanging

**T+2m**: Memory leak on api-0
- api-0 starts allocating 30MB/min
- Memory fills toward 85% limit
- GC becomes more aggressive
- Requests become sporadic and slow

**T+3-5m**: Complete cascade
- API-0 at 85% memory; nearly all requests timeout
- Pool exhaustion on API-1 (network latency)
- DB still saturated (CPU 90%)
- ~30% of requests fail with HTTP 503 "Service Unavailable"

### Trainee Diagnostic Path

**Step 1: Identify the symptoms**
```bash
# Check HTTP response codes
$ curl -w '%{http_code}' http://172.16.0.2/api/v1/health
200
$ curl -w '%{http_code}' http://172.16.1.2/api/v1/health
000  # timeout, connection refused

# Check load on all VMs
$ for i in 0 1 2; do
    echo "=== api-$i ==="
    ssh root@172.16.$(($i)).2 'uptime; free -h; df -h /'
  done
```

**Step 2: Isolate the root cause (not the symptoms)**
```bash
# Database CPU is the root cause, not a symptom
$ ssh root@172.16.2.2 'top -bn1 | head -20'
# Shows 90% CPU usage on postgres

# Check database connection count
$ ssh root@172.16.2.2 'psql -U postgres -c "SELECT count(*) FROM pg_stat_activity;"'
100  # or even max_connections hit

# Compare with API nodes
$ ssh root@172.16.0.2 'top -bn1 | head -3'
# Shows high system time (I/O wait), not high CPU
# This indicates CPU is NOT the problem here
```

**Step 3: Diagnose connection exhaustion**
```bash
# Check established connections from API-0 to DB
$ ssh root@172.16.0.2 'netstat -tnp | grep 172.16.2.2 | wc -l'
100  # Connection pool size

# Check connection states
$ ssh root@172.16.0.2 'netstat -tnp | grep 172.16.2.2 | awk "{print $6}" | sort | uniq -c'
50 ESTABLISHED
45 TIME_WAIT
5 SYN_SENT  # <-- These are waiting to connect

# This shows many connections stuck in SYN_SENT (slow to establish)
```

**Step 4: Identify network latency**
```bash
# Measure latency to DB
$ ssh root@172.16.0.2 'ping -c5 172.16.2.2'
--- 172.16.2.2 statistics ---
min/avg/max/stddev = 300/350/400/35 ms  # Way too high!

# Compare with API-1 which may not have latency if tier-level
$ ssh root@172.16.1.2 'ping -c5 172.16.2.2'
min/avg/max/stddev = 10/12/15/2 ms

# Or check both (if latency is random between nodes)
# This reveals: API tier has network fault injected
```

**Step 5: Check memory on api-0**
```bash
$ ssh root@172.16.0.2 'free -h'
              total        used      free
Mem:          512Mi       432Mi      80Mi  # 84% used!

$ ssh root@172.16.0.2 'pmap -x $(pidof app-server) | tail -1'
# Shows memory allocation growing

$ ssh root@172.16.1.2 'free -h'
              total        used      free
Mem:          512Mi       250Mi      262Mi  # 49% used (normal)
```

### Remediation (Ordered by Urgency)

**1. Stop the CPU fault first (root cause)**
```bash
curl -X POST http://172.16.2.2:7777/fault/stop \
  -H "Content-Type: application/json" \
  -d '{"id":"db-cpu-spike"}'
# DB CPU drops immediately; query processing restores

# Verify
$ ssh root@172.16.2.2 'top -bn1 | head -2'
# CPU down to 10%
```

**2. Stop network latency (removes cascading effect)**
```bash
# On api-0
curl -X POST http://172.16.0.2:7777/fault/stop \
  -H "Content-Type: application/json" \
  -d '{"id":"api-db-latency"}'

# On api-1
curl -X POST http://172.16.1.2:7777/fault/stop \
  -H "Content-Type: application/json" \
  -d '{"id":"api-db-latency"}'

# Verify latency restored
$ ssh root@172.16.0.2 'ping -c5 172.16.2.2'
min/avg/max/stddev = 10/11/13/1 ms  # Back to normal
```

**3. Stop memory leak**
```bash
curl -X POST http://172.16.0.2:7777/fault/stop \
  -H "Content-Type: application/json" \
  -d '{"id":"api0-memleak"}'

# Memory will not be released immediately, but will stabilize
# App-server won't keep growing
```

**4. Monitor recovery**
```bash
# API latency improves as pool drains
$ time curl http://172.16.0.2/api/v1/slow-endpoint
real    2.5s  # Was 30s at height of cascade

# Connection pool refills
$ ssh root@172.16.0.2 'netstat -tnp | grep ESTABLISHED | wc -l'
10  # Down from 50, back to normal pool size

# Error rate drops
$ curl http://172.16.0.2/metrics | grep 'http_requests_total.*503'
# Value stops increasing
```

### Scoring
- **Base**: 100 points
- **Time penalty**: -0.033 points/sec (cascading penalty is higher)
- **Hint penalty**: -10 points each
- **Perfect window**: 8 minutes
- **Full recovery**: All three faults stopped + metrics below thresholds

### Teaching Points
- **Order matters**: Remove root cause first, not symptoms
- **Cascading failures**: One problem multiplies through dependencies
- **Metrics tell a story**:
  - High latency → suspect network or slow upstream
  - High I/O wait % → suspect disk or network, not CPU
  - High SYS time → mutex contention or system calls
- **Tool limitations**: `top` doesn't show connection states; use `netstat`
- **Connection exhaustion symptoms**: Timeouts, 503, and "too many connections" appear AFTER the root cause
- **Time windows matter**: Even stopping root cause takes time for cascade to unwind (connection drains)

---

## SCENARIO 3: Memory Leak with OOM Kill (Medium)

### Setup
```yaml
faults:
  - type: memory
    target: {vm: app-0}
    params: {rate: "50mb/min", ceiling: "90%"}
    at: 0s
    duration: 10m
```

### What Trainee Observes

**T+0 to T+3m**: Silent degradation
```bash
$ watch -n 1 'free -h'
# Memory starts at 100/512 MB
# Every minute: +50 MB
# T+1m: 150 MB
# T+2m: 200 MB
# T+3m: 250 MB

# Application still responsive; no errors yet
```

**T+3 to T+8m**: Gradual slowdown
```bash
# Memory hits 450 MB (88%)
$ ssh root@172.16.0.2 'ps aux | head'
# Shows many defunct processes [app-server]

# Latency increases mysteriously
$ curl -w "Time: %{time_total}s\n" http://172.16.0.2/
# T+3m: 50ms
# T+5m: 500ms (10x slower!)
# T+7m: 2000ms (timeouts)
```

**T+8m**: OOM killer activates
```
[   1234.567] Memory pressure on node ... exceeding limit
[   1234.568] oom-kill: constraint=CONSTRAINT_NONE, ...
[   1234.569] Killed process 1234 (app-server) total-vm:450000kB, ...
```

Application suddenly dies; curl returns "Connection refused"

### Diagnostic Steps
```bash
# Check memory usage trend
$ ssh root@172.16.0.2 'watch -n 5 "free -h && echo '---' && ps aux | head -5"'
# Observe memory creeping up

# Check for memory leak sources
$ ssh root@172.16.0.2 'pmap -x $(pidof app-server)'
# Should show memory map; leaks show unbounded growth

# Check OOM events in dmesg
$ ssh root@172.16.0.2 'dmesg | tail -20'
# Look for "Killed process" line

# Check if process still running
$ ssh root@172.16.0.2 'ps aux | grep app-server'
# Empty (process killed)

# Check systemd status
$ ssh root@172.16.0.2 'systemctl status app-server'
failed
# Will show exit code 137 (killed by SIGKILL from OOM)
```

### Root Cause Identification
```bash
# This is a simulated fault, not a code bug
$ curl -X GET http://172.16.0.2:7777/health
{"status":"ok","faults":[{"id":"mem-leak","type":"memory","params":{...}}]}

# The onfire-agent is running the memory leak
# Check agent logs
$ ssh root@172.16.0.2 'journalctl -u onfire-agent -f'
```

### Remediation
**Option 1: Stop the fault (quick fix)**
```bash
curl -X POST http://172.16.0.2:7777/fault/stop \
  -H "Content-Type: application/json" \
  -d '{"id":"mem-leak"}'

# Agent stops allocating memory
# Memory stabilizes at ~450 MB (won't shrink immediately)
# Application may be already dead (OOM killed)
```

**Option 2: Restart the application (if still alive)**
```bash
$ ssh root@172.16.0.2 'systemctl restart app-server'
# Process restarts fresh with minimal memory
# Memory resets to 50 MB baseline
```

**Option 3: Increase memory ceiling in scenario (cheating - for testing only)**
```bash
# Modify fault params to allow higher ceiling
# Not viable in real scenario; teaches to fix root cause
```

### Monitoring During Recovery
```bash
# After stopping fault
$ ssh root@172.16.0.2 'free -h'
# Memory should plateau

# If OOM killed the app, restart it
$ ssh root@172.16.0.2 'systemctl start app-server'

# Verify service is healthy
$ curl http://172.16.0.2/healthz
200 OK
```

### Scoring
- **Base**: 100 points
- **Penalty**:
  - -1 point/second while memory climbing
  - -20 points if OOM killer fires (unavoidable if not caught early)
- **Success**: Stop fault + restart app-server
- **Perfect**: Identify leak <3 min and stop fault before OOM

### Teaching Points
- **Memory leaks are subtle**: No immediate error; just slow degradation
- **Latency precedes failure**: Check metrics before they fail
- **Swap thrashing**: Even with swap, service becomes unusable long before OOM
- **OOM kill is nuclear option**: No graceful shutdown; data loss possible
- **Tools**:
  - `free -h`: Simple trend observation
  - `pmap -x`: See what's hogging memory
  - `dmesg`: Find OOM kill evidence
  - `systemctl`: Check service state after crash
- **Prevention**: Memory limits (cgroups), monitoring, alerts at 70% and 85%

---

## SCENARIO 4: Network Partition & Cascade (Expert)

### Setup
```yaml
scenario:
  title: "The Great Network Partition"
  difficulty: expert

environment:
  tiers:
    - name: frontend
      count: 1
    - name: middleware
      count: 2
    - name: backend
      count: 1

faults:
  # Network partition: Frontend ↔ Middleware has 90% packet loss
  - type: network
    target: {tier: frontend}
    params: {action: loss, packet_loss: 90%}
    at: 0s
```

### Expected Behavior

**At T+0s**:
- Frontend sends requests to Middleware
- 90% of packets dropped
- ~10% of requests succeed by luck
- 90% fail with "Connection timed out"

**At T+30s**:
- Middleware queues back up (requests accumulating)
- Middleware memory grows
- Backend traffic decreases (requests stuck upstream)

**At T+2m**:
- Frontend gives up; circuit breaker opens
- Requests rejected with 503 (Service Unavailable)
- Cascade begins

### Multi-Layer Diagnostic Approach

**Layer 1: Application metrics**
```bash
$ curl http://frontend:8080/metrics
http_requests_total{status="503"} 3450  # Growing

# Check latency percentiles
$ curl http://frontend:8080/metrics | grep http_request_duration_seconds
# p99: 30000ms (30 second timeout!)
# p95: 25000ms
# p50: 100ms (lucky 10%)
```

**Layer 2: Network diagnosis**
```bash
# From frontend, try to reach middleware
$ ssh root@172.16.0.2 'ping -c 100 172.16.1.2 | tail -3'
100 packets transmitted, 10 received, 90% packet loss, time 1000ms

# From middleware, check incoming packet loss
$ ssh root@172.16.1.2 'ethtool -S eth0 | grep -i drop'
RX_dropped: 9000  # Lots of drops

# Check if it's ingress or egress
$ ssh root@172.16.0.2 'tc qdisc show dev eth0'
qdisc netem 800d: root refcnt 2 loss 90%
# Confirms: loss rule is on OUTGOING (frontend sending)

# On middleware side
$ ssh root@172.16.1.2 'tc qdisc show dev eth0'
# Should be clean (no loss rule)
```

**Layer 3: TCP connection state**
```bash
# On frontend
$ ssh root@172.16.0.2 'netstat -tn | grep 172.16.1.2'
# Mostly SYN_SENT (trying to connect but packets lost)
# Some ESTABLISHED (lucky handshakes)

# On middleware
$ ssh root@172.16.1.2 'netstat -tn | grep 172.16.0.2'
# Mostly SYN_RECV (waiting for final ACK that never arrives)
```

**Layer 4: Application-level symptoms**
```bash
# Check middleware queue buildup
$ ssh root@172.16.1.2 'curl http://localhost:8080/metrics | grep queue_depth'
queue_depth: 5000  # Should be <100

# Check memory pressure
$ ssh root@172.16.1.2 'free -h'
# Memory usage rising (requests queued in memory)

# Check backend traffic (should be low)
$ ssh root@172.16.2.2 'curl http://localhost:8080/metrics | grep http_requests_total'
# Should be far lower than frontend attempted requests
```

### Root Cause Analysis Path

**Question 1: Is it one-way or two-way?**
```bash
# Ping middleware from frontend
$ ping -c 5 172.16.1.2
# 90% loss

# Ping frontend from middleware
$ ping -c 5 172.16.0.2
# 0% loss (traffic goes through!)
# Conclusion: ONE-WAY failure (frontend→middleware broken)
```

**Question 2: Is it network, application, or database?**
```bash
# Application layer is fine
$ ssh root@172.16.1.2 'curl http://localhost:8080/healthz'
200 OK

# Database is fine
$ ssh root@172.16.1.2 'curl http://172.16.2.2:5432 2>&1'
# Can connect to backend

# Network is the problem
$ tc qdisc show dev eth0
# Loss rule present on frontend eth0
```

**Question 3: Is it a temporary blip or permanent?**
```bash
# Monitor for duration
$ for i in {1..10}; do
  ping -c 10 172.16.1.2 | grep '% packet loss'
  sleep 10
done
# Consistent 90% loss for 100 seconds
# Conclusion: Permanent fault injection, not transient
```

### Remediation

**Identify the fault location**:
```bash
# The fault must be on one of three VMs
$ curl http://172.16.0.2:7777/health  # Frontend
$ curl http://172.16.1.2:7777/health  # Middleware
$ curl http://172.16.2.2:7777/health  # Backend

# Frontend will show the network fault
$ curl http://172.16.0.2:7777/health
{
  "faults": [
    {"id": "fe-mw-loss", "type": "network", "params": {"action": "loss", "packet_loss": "90%"}}
  ]
}
```

**Stop the fault**:
```bash
curl -X POST http://172.16.0.2:7777/fault/stop \
  -H "Content-Type: application/json" \
  -d '{"id":"fe-mw-loss"}'

# Verify
$ tc qdisc show dev eth0
# Should show nothing (qdisc removed)

$ ping -c 10 172.16.1.2
# Should be ~0% loss now
```

**Monitor recovery**:
```bash
# Middleware queue drains
$ watch -n 1 'curl -s http://172.16.1.2:8080/metrics | grep queue_depth'
# Should drop to <100

# Frontend success rate increases
$ watch -n 1 'curl -s http://frontend:8080/metrics | grep "status=\"200\""'
# Should increase (or switch to 503 briefly as circuit opens)

# Memory normalizes
$ ssh root@172.16.1.2 'free -h'
# Should drop as queue empties
```

### Teaching Points
- **Network faults are directional**: Packet loss can be one-way
- **Cascade is delayed**: Takes minutes for upstream queues to fill
- **Monitoring layers**: Need app metrics + network metrics + system metrics
- **Distinguishing layers**:
  - High latency + low error rate = network
  - Medium latency + rising error rate = queue buildup
  - Rapid error increase = cascading failure starting
- **Quick diagnosis**:
  1. Ping (round-trip latency)
  2. `tc qdisc show` (check for rules)
  3. `netstat` (check connection states)
  4. Application metrics (queue depth, latency percentiles)
- **Recovery is not instant**: Queues drain, circuit breakers reset, connections re-establish

---

## SCENARIO 5: DNS Hijack & Service Misdirection (Hard)

### Setup
```yaml
faults:
  - type: dns
    target: {vm: app-0}
    params:
      record: "db.internal"
      resolve_to: "192.0.2.1"  # Non-existent server
    at: 0s
```

### What Trainee Sees

**Symptom 1: Application can't connect to database**
```bash
$ curl http://app-0:8080/healthz
503 Service Unavailable

# Logs show:
# ERROR: failed to connect to db.internal: connect timeout
```

**Symptom 2: Service works when IP is hardcoded**
```bash
# In config file, if you change:
# db_host = "db.internal"  # broken
# db_host = "172.16.2.2"   # works directly

# Then service recovers
# This is a clue it's DNS
```

**Symptom 3: Other services fine**
```bash
$ curl http://app-1:8080/healthz
200 OK

# app-1 still works (no fault on that VM)
# This tells you it's VM-specific, not platform-wide
```

### Diagnostic Steps

**Step 1: Test DNS resolution**
```bash
$ ssh root@172.16.0.2 'nslookup db.internal'
Server:  8.8.8.8
Address: 8.8.8.8#53

Name:    db.internal
Address: 192.0.2.1

# 192.0.2.1 is not a real server (TEST-NET-1, RFC 5737)
# This is the hijack!
```

**Step 2: Check /etc/hosts**
```bash
$ ssh root@172.16.0.2 'cat /etc/hosts'
127.0.0.1   localhost
::1         localhost
192.0.2.1   db.internal # onfire-fault  <-- THE CULPRIT

# Compare with app-1
$ ssh root@172.16.1.2 'cat /etc/hosts'
127.0.0.1   localhost
::1         localhost
# No hijack entry

# Or entire network:
$ ssh root@172.16.2.2 'cat /etc/hosts'
# Also clean
```

**Step 3: Verify connectivity**
```bash
# Try to reach the hijacked IP
$ ssh root@172.16.0.2 'ping -c 3 192.0.2.1'
PING 192.0.2.1 ...
no answer from 192.0.2.1
# Non-existent (as expected for TEST-NET)

# Real database is still up
$ ssh root@172.16.0.2 'ping 172.16.2.2'
PING 172.16.2.2 ...
64 bytes from 172.16.2.2: icmp_seq=1 ttl=64 time=0.5ms
# Works fine
```

### Root Cause Discovery

**The fault is in onfire-agent**:
```bash
$ curl http://172.16.0.2:7777/health
{
  "faults": [
    {"id": "dns-hijack", "type": "dns", "params": {
      "record": "db.internal",
      "resolve_to": "192.0.2.1"
    }}
  ]
}
```

### Remediation

**Option 1: Stop the DNS fault**
```bash
curl -X POST http://172.16.0.2:7777/fault/stop \
  -H "Content-Type: application/json" \
  -d '{"id":"dns-hijack"}'

# Verify /etc/hosts is cleaned
$ ssh root@172.16.0.2 'grep onfire-fault /etc/hosts'
# Should return nothing

# Re-resolve
$ ssh root@172.16.0.2 'nslookup db.internal'
Server: 8.8.8.8
Address: 8.8.8.8#53

Name: db.internal
Address: 172.16.2.2  # Real server!
```

**Option 2: Manual fix (if agent broken)**
```bash
# Remove the line manually
$ ssh root@172.16.0.2 'sed -i /onfire-fault/d /etc/hosts'

# Flush DNS cache (systemd-resolved)
$ ssh root@172.16.0.2 'systemctl restart systemd-resolved'
```

**Option 3: Use IP directly (workaround)**
```bash
# Configure app to use IP instead of hostname
# But this doesn't solve the problem for future deployments
```

### Verification
```bash
# Service recovers
$ curl http://app-0:8080/healthz
200 OK

# Database connection restored
$ curl http://app-0:8080/api/users
200 [{"id":1, "name": "Alice"}, ...]

# Logs show successful connection
$ ssh root@172.16.0.2 'tail /var/log/app.log'
# Connected to db.internal (172.16.2.2)
```

### Teaching Points
- **DNS issues manifest as connectivity problems**: "Can't reach X" not "DNS broken"
- **One-VM vs platform-wide**: Helps identify scope (local DNS vs resolver)
- **Hijack vs lookup failure**:
  - Lookup failure → "Name or service not known"
  - Hijack → Resolves to wrong IP → Connection timeout
- **Troubleshooting tools**:
  - `nslookup` / `dig` → Check resolution
  - `/etc/hosts` → Check for hijacks
  - `ping IP` vs `ping hostname` → Separate network vs DNS
  - Application logs → Show which host it tried to connect to
- **Real-world parallels**:
  - DNS cache poisoning
  - BGP hijack (wrong IP advertised)
  - Load balancer misconfiguration (wrong backend IP)
  - Host file manipulation (malware)

---

## Appendix: Diagnostic Toolkit Cheat Sheet

### Quick Diagnostics by Symptom

| Symptom | First Command | Second Command | Root Cause Likely |
|---|---|---|---|
| Service won't start | `systemctl status svc` | `journalctl -u svc -n 50` | Permission, dependency, missing file |
| Service times out | `curl -v http://host:port/` | `netstat -tn \| grep port` | Connection refused, firewall, service down |
| High latency | `curl -w "%{time_total}\n"` | `ping -c 10 upstream` | Network latency, CPU saturation, disk I/O |
| Can't reach host | `ping IP` | `nslookup hostname` | Network/routing vs DNS |
| Memory grows unbounded | `free -h` | `pmap -x $(pidof proc)` | Memory leak, caching, buffer bloat |
| Disk full | `df -h` | `du -sh /* \| sort -rh` | Large log, temp, cache, or fault file |
| High CPU | `top` | `ps aux \| head` | Process runaway, compute workload, busy loop |
| Connection refused | `telnet host port` | `netstat -tln \| grep port` | Service down, wrong port, firewall |
| Slow I/O | `iostat -x 1` | `dmesg \| tail -20` | High queue depth, throttling, error |
| Cascade failure | Check error rate | Check upstream | Likely upstream timeout or saturation |

### Essential Network Diagnostics
```bash
# Connectivity
ping -c 5 <IP>                    # Basic connectivity
traceroute -m 15 <IP>            # Routing path
mtr -c 100 <IP>                  # Continuous latency

# DNS
nslookup <hostname>              # Simple lookup
dig <hostname> @<dns-server>     # Full DNS query
getent hosts <hostname>          # System resolver

# TCP/UDP connections
netstat -tnp | grep <port>       # TCP state
ss -tnp | grep <port>            # Modern TCP state
lsof -i :<port>                  # Process on port

# Traffic
tcpdump -i eth0 -n host <IP>     # Capture packets
iptables -t nat -L -nv           # NAT rules
tc qdisc show dev eth0           # Kernel queuing

# Routing
ip route show                     # Routing table
arp -an                           # ARP cache
ip link show                      # Interface status
```

### Essential System Diagnostics
```bash
# Memory
free -h                          # Quick overview
ps aux --sort=-%mem | head       # Top memory users
/proc/meminfo                    # Detailed breakdown

# CPU
top -bn1 | head -20              # Quick snapshot
uptime                           # Load average
cat /proc/cpuinfo                # CPU details

# Disk
df -h                            # Filesystem usage
du -sh /* | sort -rh             # Directory sizes
iostat -x 1 5                    # Disk I/O stats
lsof | grep <path>              # Open files on mount

# Processes
ps aux | grep <name>             # Find process
pmap -x <PID>                    # Process memory map
strace -p <PID>                  # System calls (slow!)
```

