# SRE Scenario Design Guide for Pudu
## Patterns for Building Training Scenarios in Firecracker MicroVM Platform

This guide provides patterns, templates, and anti-patterns for creating effective SRE training scenarios in pudu.

---

## 1. Scenario Design Principles

### Core Goals
1. **Single Root Cause per Easy scenario** - Trainee learns to diagnose one failure mode
2. **Cascading Failures in Medium/Hard** - Teaches root cause vs symptom distinction
3. **Time Pressure** - Realistic SLA windows (5-15 minutes depending on difficulty)
4. **Ambiguity** - Real incidents are messy; avoid "obvious" next steps
5. **Measurable Recovery** - Objectives must be checkable programmatically

### Difficulty Progression
| Difficulty | Root Causes | Failure Time | Investigation Depth | Typical Duration |
|---|---|---|---|---|
| **Easy** | 1 | Immediate (T+0) | 1-2 tools | 3-5 minutes |
| **Medium** | 2-3 | Staggered (T+0, T+1m, T+2m) | 3-4 tools, cross-VM | 8-15 minutes |
| **Hard** | 3-5 | Delayed cascade | 5+ tools, multi-tier analysis | 15-30 minutes |
| **Expert** | 5+ | Emergent behavior | Mastery required; non-obvious chain | 30+ minutes |

---

## 2. Scenario Architecture Patterns

### Pattern 1: Monolith (Single VM, Single Failure)
**Best for**: Easy difficulties, teaching diagnostics on single target

```yaml
scenario:
  architecture: monolith
  difficulty: easy

environment:
  tiers:
    - name: app
      count: 1
      vcpus: 1
      mem_mb: 512

faults:
  - id: single-fault
    type: <one of: cpu, memory, disk, network, process, dns>
    target:
      tier: app
    at: 0s
```

**Training Focus**:
- Basic metric collection (df, top, netstat)
- Single VM SSH/API troubleshooting
- Identifying root cause from symptoms

**Examples**:
- Disk full → high error rate
- Memory leak → OOM kill
- Process crash → 502 responses
- DNS hijack → connection refused

### Pattern 2: Tier-Based Cascade (2-3 tiers, 1-2 root causes)
**Best for**: Medium difficulty, teaching layer identification

```yaml
scenario:
  architecture: microservices
  difficulty: medium

environment:
  tiers:
    - name: frontend
      count: 1
    - name: backend
      count: 1

faults:
  # Root cause: Backend overloaded
  - id: root-cause
    type: cpu
    target: {tier: backend}
    params: {load: "90%"}
    at: 0s

  # Secondary effect: Frontend timeout
  - id: symptom-timeout
    type: network
    target: {tier: frontend}
    params: {action: delay, latency: 500ms}
    at: 1m
```

**Training Focus**:
- Distinguish root cause from symptoms
- Cross-VM investigation
- Understanding dependency chains
- Prioritizing remediation

**Key: Stagger fault injection**
- T+0: Root cause appears (subtle)
- T+1min: Secondary effects appear (obvious)
- Trainee discovers primary issue only after investigating secondary

### Pattern 3: Emergent Cascade (Single fault → Multi-tier impact)
**Best for**: Hard/Expert difficulty, realistic chaos

```yaml
scenario:
  architecture: microservices
  difficulty: hard

environment:
  tiers:
    - name: api
      count: 2
    - name: cache
      count: 1
    - name: db
      count: 1

faults:
  # Single fault: DB CPU spike
  - id: db-spike
    type: cpu
    target: {tier: db}
    params: {load: "95%"}
    at: 0s
    duration: 5m
    # No other faults injected—cascade emerges naturally!
```

**What Happens**:
1. DB CPU spike → slower queries
2. API threads wait longer for responses → thread pool saturated
3. Thread pool full → requests rejected (503)
4. Cache effectiveness drops (longer DB queries) → cache churn
5. Cache layer memory grows → potential OOM if not bounded
6. API error rate climbs

**Training Focus**:
- Emergent behavior from single root cause
- Complex dependency chains
- When to accept degradation vs restore full capacity
- Monitoring during recovery

---

## 3. Specific Failure Scenario Templates

### Template 1: Resource Exhaustion (Easy)

```yaml
scenario:
  id: exhaust-disk-001
  title: "Disk Space Crisis"
  difficulty: easy
  architecture: monolith

environment:
  tiers:
    - name: app
      count: 1
      mem_mb: 512
      setup:
        - systemctl enable nginx
        - systemctl start nginx

faults:
  - id: disk-fill
    type: disk
    target: {tier: app}
    params: {path: "/"}
    at: 0s

signals:
  alerts:
    - name: DiskCritical
      severity: critical
      fired_at: 0s
      message: "Disk usage >95%"
  symptoms:
    - "nginx returns 502 Bad Gateway"
    - "New deployments fail with 'no space left on device'"
    - "Application logs stopped writing"

objectives:
  - id: disk-recovered
    description: "Disk usage <80%"
    check:
      type: agent-metric
      metric: disk_used_pct
      condition: "< 80"

hints:
  - "Check disk usage: df -h"
  - "Find large files: du -sh /* 2>/dev/null | sort -rh | head -10"
  - "A hidden .pudu-* file is the culprit"

scoring:
  base: 100
  time_penalty_per_second: 0.05
  hint_penalty: 10
  perfect_window: 5m
```

**Key Features**:
- Single metric check (disk_used_pct)
- One obvious failure (disk full)
- Quick recovery path (delete file)
- Clear hints if needed

### Template 2: Cascading Service Degradation (Medium)

```yaml
scenario:
  id: cascade-db-001
  title: "Database Connection Exhaustion"
  difficulty: medium
  architecture: microservices

environment:
  tiers:
    - name: api
      count: 2
      services: [nginx]
    - name: db
      count: 1
      services: [postgresql]

faults:
  # Stage 1 (T+0): Database overload
  - id: db-cpu-spike
    type: cpu
    target: {tier: db}
    params: {load: "90%"}
    at: 0s
    duration: 3m

  # Stage 2 (T+1m): Network latency cascades effect
  - id: api-latency
    type: network
    target: {tier: api}
    params: {action: delay, latency: 300ms}
    at: 1m
    duration: 5m

  # Stage 3 (T+2m): Memory pressure from connection leak simulation
  - id: api-memleak
    type: memory
    target: {vm: api-0}
    params: {rate: "30mb/min", ceiling: "85%"}
    at: 2m

signals:
  alerts:
    - name: APILatencyHigh
      fired_at: 0s
      message: "p99 latency >2s"
    - name: DBCPUHigh
      fired_at: 0s
      message: "Database CPU >80%"
    - name: APIMemoryHigh
      fired_at: 2m
      message: "api-0 memory >75%"
  symptoms:
    - "~30% of API requests return 503"
    - "Database logs: 'too many connections'"
    - "Response time variance (api-0 vs api-1 differ by 10x)"

objectives:
  - id: db-load-normal
    description: "DB CPU <2.0 load"
    check:
      type: agent-metric
      target: {vm: db-0}
      metric: load_avg_1
      condition: "< 2.0"

  - id: api-latency-normal
    description: "API response <500ms"
    check:
      type: http
      target: {tier: api}
      path: /api/healthz
      expected_status: 200
      # (Implicit: response time <500ms)

  - id: api0-mem-normal
    description: "api-0 memory <70%"
    check:
      type: agent-metric
      target: {vm: api-0}
      metric: mem_used_pct
      condition: "< 70"

hints:
  - "Identify which layer is the root cause: check all 3 tiers' CPU"
  - "Root cause: DB CPU is primary; others are secondary effects"
  - "Stop db-cpu-spike first, then api-latency, then api0-memleak"
  - "After stopping faults, api-0 may need restart to clear memory"

scoring:
  base: 100
  time_penalty_per_second: 0.033  # Higher penalty for medium
  hint_penalty: 15
  perfect_window: 8m

narrative: |
  Alarms are firing. The API is returning 503 errors at 30% rate.
  Database is reporting connection pool exhaustion.
  Your junior just paged you. Time's running out—SLA breach in 10 minutes.

  What do you check first?
```

**Key Features**:
- Multiple root causes injected staggered
- All objectives require specific metric thresholds
- Hints guide trainee to diagnosis, not answer
- Narrative adds urgency (realistic on-call context)

### Template 3: Complex Distributed System (Hard)

```yaml
scenario:
  id: complex-cache-001
  title: "Cache Coherency Breakdown"
  difficulty: hard
  architecture: microservices

environment:
  tiers:
    - name: frontend
      count: 2
    - name: api
      count: 2
    - name: cache
      count: 1
    - name: db
      count: 1
    - name: worker
      count: 1

faults:
  # Fault 1: Cache becomes slow (memory pressure)
  - id: cache-pressure
    type: memory
    target: {tier: cache}
    params: {rate: "50mb/min", ceiling: "80%"}
    at: 0s
    duration: 8m

  # Fault 2: DB experiences network flakiness
  - id: db-flakiness
    type: network
    target: {tier: db}
    params: {action: loss, packet_loss: 5%}
    at: 1m
    duration: 6m

  # Fault 3: Worker process crashes (cascades to queue buildup)
  - id: worker-crash
    type: process
    target: {vm: worker-0}
    params: {service: background-worker, action: stop}
    at: 3m

signals:
  alerts:
    - name: CacheMissRateHigh
      fired_at: 1m
      message: "Cache miss rate >50% (was 5%)"
    - name: APISlow
      fired_at: 2m
      message: "API p99 latency >3s"
    - name: WorkerQueueBacklog
      fired_at: 4m
      message: "Job queue depth >10k (unbounded growth)"
  symptoms:
    - "User reports: search is slow"
    - "Dashboard shows degradation starting at T+1m"
    - "Cache hit rate dropped significantly"
    - "Background jobs not processing"
    - "Database connection pool fills occasionally"

objectives:
  - id: cache-memory-normal
    description: "Cache memory usage <70%"
    check:
      type: agent-metric
      target: {tier: cache}
      metric: mem_used_pct
      condition: "< 70"

  - id: cache-hitrate-recovered
    description: "Cache hit rate >80%"
    check:
      type: http
      target: {tier: api}
      path: /metrics
      expected_status: 200
      # Application-specific metric check

  - id: worker-processing
    description: "Worker queue depth <100"
    check:
      type: http
      target: {tier: worker}
      path: /metrics
      expected_status: 200

  - id: api-latency
    description: "API p50 latency <100ms"
    check:
      type: http
      target: {tier: api}
      path: /api/search?q=test
      expected_status: 200

hints:
  - "Three separate issues are unfolding independently"
  - "Worker queue growth is a red herring—fix the process first"
  - "Cache memory pressure is primary; packet loss is secondary"
  - "Recovery must be orchestrated in order: worker, cache, network"
  - "After fixing faults, cache coherency takes time to recover"

scoring:
  base: 100
  time_penalty_per_second: 0.02
  hint_penalty: 20
  perfect_window: 15m

narrative: |
  Multiple systems degrading simultaneously. Your observability dashboard
  is lighting up like a Christmas tree. Senior incident commander is asking
  for ETA. Which failure is real and which are consequences?
```

**Key Features**:
- 5-tier architecture (realistic complexity)
- 3 independent but correlated faults
- Objectives require detailed metrics
- Recovery order matters (orchestration)
- Hard to diagnose without systematic approach

---

## 4. Anti-Patterns (What NOT to Do)

### ❌ Anti-Pattern 1: Too Many Simultaneous Faults
```yaml
# BAD: All faults at T+0, no staggering
faults:
  - type: cpu
    target: {tier: api}
    params: {load: "90%"}
    at: 0s

  - type: memory
    target: {tier: cache}
    params: {rate: "50mb/min"}
    at: 0s

  - type: disk
    target: {tier: db}
    params: {path: "/"}
    at: 0s
```

**Problem**: Trainee cannot isolate causes; becomes guessing game

**Fix**: Stagger faults (T+0, T+1m, T+2m)

---

### ❌ Anti-Pattern 2: Unmeasurable Objectives
```yaml
# BAD: Too vague
objectives:
  - id: "service-healthy"
    description: "Service is working"
    check:
      type: http
      target: {tier: api}
      path: /
      expected_status: 200
      # How long should response take?
      # Should all nodes be healthy?
```

**Problem**: Scenario end condition unclear; trainee unsure when recovered

**Fix**: Specify metrics explicitly
```yaml
objectives:
  - id: "api-latency-normal"
    description: "All API nodes respond in <200ms"
    check:
      type: http
      target: {tier: api}
      path: /api/healthz
      expected_status: 200
      # Additional implicit check: response_time < 200ms
```

---

### ❌ Anti-Pattern 3: Obvious Failure Mode
```yaml
# BAD: Service crashes immediately and obviously
faults:
  - type: process
    target: {service: nginx}
    action: stop
    at: 0s

# Trainee sees "Connection refused" and immediately knows cause
```

**Problem**: No diagnosis needed; not training troubleshooting

**Fix**: Make root cause subtle, symptoms obvious
```yaml
# GOOD: Root cause is not immediately visible
faults:
  - type: memory
    target: {vm: app-0}
    params: {rate: "40mb/min"}
    at: 0s

# Trainee sees latency increase gradually
# Diagnosis requires memory leak detection tools
# Root cause discovery takes 5+ minutes
```

---

### ❌ Anti-Pattern 4: Hints Are The Answer
```yaml
# BAD: Hints give away the answer
hints:
  - "Remove the .pudu-diskfill file to recover disk space"
  - "Run: rm /.pudu-diskfill"
  - "Then run: systemctl restart nginx"

# Trainee just follows hints; no learning
```

**Fix**: Hints guide thinking, not implementation
```yaml
hints:
  - "Check disk usage with: df -h"
  - "Find large files: du -sh /* 2>/dev/null | sort -rh | head"
  - "Look for hidden files (starting with .)"

# Trainee must:
# 1. Run df, see high usage
# 2. Run du, identify .pudu-diskfill
# 3. Decide to remove it (learning moment)
```

---

### ❌ Anti-Pattern 5: Unrecoverable Faults
```yaml
# BAD: No way to fix without restart
faults:
  - type: filesystem-corruption
    # No fault stop mechanism implemented
    at: 0s

# Trainee discovers corruption but cannot recover
# Scenario becomes stuck (no success path)
```

**Fix**: Ensure all faults are reversible
```yaml
# GOOD: Faults start and stop cleanly
faults:
  - type: disk
    at: 0s
    # When stopped, the .pudu-diskfill file is cleaned up
    # Recovery is possible
```

---

### ❌ Anti-Pattern 6: Time Pressure Unrealistic
```yaml
# BAD: Too much time for hard scenario
scoring:
  perfect_window: 1h  # Too much time; no urgency

# BAD: Too little for complex scenario
scoring:
  perfect_window: 2m  # Not enough for multi-tier diagnosis
```

**Fix**: Time based on complexity
| Difficulty | Time | Notes |
|---|---|---|
| Easy | 5m | Single failure, obvious path |
| Medium | 10m | 2-3 components, 3-4 diagnostics |
| Hard | 20m | 4+ components, complex chain |
| Expert | 30m | Emergent behavior, ambiguity |

---

## 5. Composition Patterns

### Small Scenario (Trainee's First Experience)
**Duration**: 3-5 min
**Architecture**: Single VM (monolith)
**Root Causes**: 1
**Learning**: One tool (df, top, systemctl, etc)
**Example**: Disk full, memory leak, process crash

```yaml
scenario:
  id: first-scenario-001
  title: "Your First On-Call"
  difficulty: easy
  architecture: monolith
  tags: [introductory, single-tier]
```

### Medium Scenario (Team Fundamentals)
**Duration**: 8-15 min
**Architecture**: 2-3 tiers
**Root Causes**: 2-3
**Learning**: Cross-VM diagnosis, prioritization
**Example**: Cascading latency, connection exhaustion, cache incoherence

```yaml
scenario:
  id: medium-scenario-002
  title: "Cascading Failures"
  difficulty: medium
  architecture: microservices
  tags: [multi-tier, cascading]
```

### Advanced Scenario (Senior SRE Prep)
**Duration**: 20-30 min
**Architecture**: 4+ tiers, complex topology
**Root Causes**: 3-5, some independent
**Learning**: Systems thinking, trade-offs, optimization
**Example**: Emergent behavior, performance tuning, capacity planning

```yaml
scenario:
  id: hard-scenario-003
  title: "Production Incident Simulation"
  difficulty: hard
  architecture: microservices
  tags: [expert, production-realistic]
```

---

## 6. Metrics & Observability Integration

### Recommended Application Metrics
For scenarios to be effective, applications should expose:

```
# HTTP endpoints
http_requests_total{status="200|5xx", endpoint="/api/*"}
http_request_duration_seconds{endpoint="/api/*", quantile="0.5|0.95|0.99"}
http_connection_pool_size{backend="db|cache"}
http_connection_pool_waiting{backend="db|cache"}

# Database
db_connections_active
db_query_duration_seconds{quantile="0.5|0.95|0.99"}
db_connection_errors_total

# Cache
cache_hits_total
cache_misses_total
cache_evictions_total
cache_memory_bytes

# Worker/Queue
job_queue_depth
job_processing_duration_seconds
job_errors_total

# Application
gc_duration_seconds
goroutines_count
memory_allocations_bytes
```

### Objective Types (Implementable)

| Type | Syntax | Example |
|---|---|---|
| agent-metric | `{metric, condition}` | `disk_used_pct < 80` |
| http | `{endpoint, expected_status}` | `/healthz → 200` |
| process-running | `{service}` | `nginx → active` |

---

## 7. Checklist for Scenario Creation

- [ ] **Narrative**: Story told from on-call engineer's perspective
- [ ] **Single root cause** (easy) or **clear causality** (medium/hard)
- [ ] **Faults staggered**: Root cause hidden by staggered injection
- [ ] **Symptoms obvious**: But root cause requires investigation
- [ ] **Objectives measurable**: Metrics, not subjective assessment
- [ ] **Recovery path clear**: But not handed to trainee in hints
- [ ] **Time realistic**: Based on complexity and diagnosis depth
- [ ] **Hints progressive**: Nudge thinking, don't solve
- [ ] **All faults reversible**: Scenario can reach success state
- [ ] **Testing performed**: Scenario tested end-to-end before use
- [ ] **VMs have tools needed**: stress-ng, tc, systemctl, etc.
- [ ] **No hardcoded IPs**: Use tier names, let platform assign IPs
- [ ] **Cloud-init setup runs**: Services start correctly on VM launch
- [ ] **Agent metrics available**: Metrics endpoint working correctly

