# SRE Incident Patterns & Infrastructure Failure Modes
## Comprehensive Guide for Firecracker-based MicroVM Orchestration

**Document Purpose**: This is a structured analysis of typical infrastructure outages, service failures, and incident patterns encountered by SREs in production systems. It focuses on failure modes relevant to microVM orchestration platforms and severity levels for incident response.

---

## 1. VM/Container Lifecycle Failures

### 1.1 VM Launch & Initialization Failures

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Kernel panic on boot** | VM never reaches running state; boot timeout; error logs show "kernel panic" | Missing kernel modules, memory corruption, incompatible kernel image, hardware limitations | Complete VM unavailable; service cannot start | Senior SRE / Eng Manager |
| **Firecracker process crash** | VMM process dies unexpectedly; logs show segfault or abort | Memory corruption in VMM, unsupported CPU flags, resource exhaustion on host, signal handling issues | Single or multiple VMs become unreachable | Mid-level SRE |
| **Cloud-init or user-data failure** | VM boots but services not configured; networking not ready; provisioning incomplete | Invalid cloud-init YAML; missing packages in ISO; script execution errors; dependency ordering | VM runs but is unusable; cascading service failures | Junior Dev / Mid-level SRE |
| **TAP device creation race condition** | VMs launch but network is unreachable; random hangs during fleet creation | Multiple VMs trying to create same TAP device; insufficient system resources; permission issues; timing window | Partial fleet unavailable; some VMs isolated | Mid-level SRE |
| **Cgroup/namespace allocation failure** | Process cannot fork; "too many open files" during scaling; resource limits hit | VM ID allocation exhaustion; file descriptor leaks; cgroup v2 misconfiguration; kernel limits | Scaling blocked; fleet deployment fails | Mid-level SRE |
| **Rootfs mount failure** | "Device or resource busy" errors; mount namespace pollution; stale mounts on host | Concurrent VM launches using same rootfs path; unclean shutdown; NFS locking issues; overlayfs corruption | VMs cannot start; data inconsistency | Senior SRE |

### 1.2 VM Termination & Cleanup Failures

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Graceful shutdown timeout** | VMM signals SIGTERM but VM doesn't exit; processes continue running; stuck in shutdown limbo | Zombie processes in VM; systemd not responding; busybox init issues; uninterruptible sleep | Fleet cleanup delayed; resources held indefinitely | Mid-level SRE |
| **Force kill leaving state** | After SIGKILL, zombie processes remain; TAP devices not cleaned up; memory not freed; PID held | SIGKILL doesn't guarantee cleanup; signal handlers ignore termination; kernel bug with certain processes | Resource leak; subsequent VMs cannot reuse same ID; cascading failures | Senior SRE |
| **Incomplete TAP device cleanup** | TAP interface remains after VM deletion; `ip link` shows orphaned devices; traffic routing breaks | Race condition in cleanup code; TAP deletion command fails silently; kernel module bug | Network isolation failure; next VM gets wrong TAP; broadcast storms | Mid-level SRE |
| **Rootfs image corruption on delete** | Copy-on-write overlays not cleaned up; stale inode references; disk space not freed | Concurrent fleet deletion; NFS lock timeout; ext4 corruption from unclean unmount | Disk fills unexpectedly; fleet recreation fails | Senior SRE |
| **Partial fleet deletion leaving orphans** | Some VMs deleted, others stuck; mixed cleanup states; inconsistent state in API | Transaction not atomic; error during loop leaves some VMs running; cancellation signal lost | Manual cleanup required; debugging difficult; API state inconsistency | Mid-level SRE |

### 1.3 VM State Tracking Failures

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Lost track of running VMs** | API shows fleet as "stopped" but VMs still running; stale process references; orphaned TAP devices | Crash in VM status polling; database corruption; network partition from monitoring | Double-launch of same ID; cascading VM failures | Mid-level SRE |
| **Mismatched VM ID allocation** | Two VMs allocated same ID; network collision; both send traffic on same TAP | Race condition in ID allocator; no locking; concurrent fleet creation | Network storms; service degradation; data corruption | Senior SRE |
| **Stale heartbeat/health check** | API thinks VM is healthy but it's actually crashed; monitoring shows no problem; service still receives requests | Network partition between host and VM; agent crash but Firecracker still running; firewall blocking agent port | Silent failures; requests timeout; cascading failures upstream | Junior Dev / Mid-level SRE |

---

## 2. Network and Connectivity Issues

### 2.1 TAP Device & Virtual Networking Failures

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **TAP device not created** | `ip link` shows no tapN device; VM cannot ping gateway; timeout on TCP connect | `ip tuntap` command fails silently; permission denied on /dev/net/tun; iproute2 not installed | Complete network isolation for that VM | Junior Dev / Mid-level SRE |
| **TAP device deleted before VM stops** | VM gets I/O errors on network; "No such device" in dmesg; sudden connection loss | Race condition in cleanup code; premature TAP deletion during Fleet.Stop(); signal ordering wrong | Service disruption; incomplete graceful shutdown; dropped connections | Mid-level SRE |
| **IP address collision** | Multiple VMs get same 172.16.N.2 address; ARP conflicts; traffic goes to wrong VM | Duplicate ID allocation; stateful IP assignment corruption; cloud-init race condition | Data corruption; cross-VM traffic leakage; security breach | Senior SRE / Eng Manager |
| **Subnet exhaustion** | Cannot create VMs beyond 256 per host (172.16.0-255.X); pool depletion | Not implemented subnet management; no pool draining on scale-down; ID reuse not working | Scaling capped at 256 VMs; cannot handle burst load | Senior SRE / Eng Manager |
| **MTU mismatch or fragmentation** | Packets drop silently; SSH hangs after banner exchange; large responses timeout | Default MTU 1500 vs. TAP device configured differently; jumbo frames not supported | Intermittent connectivity; hard-to-debug application hangs | Mid-level SRE |
| **Multicast/broadcast storm** | Network flooding; packet loss for all VMs; CPU spike on host | ARP lookups flooded; VMs broadcast excessively; no broadcast limiting on TAP; misconfigured VLAN | Complete network degradation; all VMs affected; DDoS-like behavior | Senior SRE |

### 2.2 Firewall & IPtables Issues

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Iptables rules not applied** | VM can ping but cannot reach external services; DNS fails; specific ports blocked | iptables command fails silently; rules cleared by another process; missing conntrack module | Service unable to reach upstream; cascading failures | Junior Dev / Mid-level SRE |
| **Iptables rules not cleaned up** | Stale rules persist after VM deletion; traffic routing to old IPs; rule explosion | No tracking of rules added; error during cleanup loop; signal interrupts cleanup | Bloated iptables ruleset; slow packet processing; routing confusion | Mid-level SRE |
| **NAT table exhaustion** | "Cannot allocate memory" errors; SNAT fails; existing connections drop | Too many concurrent connections; conntrack limit reached; memory pressure | Service becomes non-functional; new connections rejected | Mid-level SRE |
| **Conntrack table full** | New TCP connections fail; "INVALID" packets dropped; established connections okay | conn_max setting too low; connection leak from applications; TIME_WAIT not recycled | Some services cannot scale; latency spikes; failed deployments | Mid-level SRE |

### 2.3 DNS Issues (Internal & External)

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **DNS hijacking / poisoning** | Services resolve to wrong IP; requests go to attacker or wrong service | `/etc/hosts` entry injected (onfire fault simulation); DNS server compromised; no DNSSEC validation | Service talking to wrong backend; data corruption; security breach | Engineering Manager |
| **DNS cache stale** | VM sees old IP for service; service migrated but VM connects to old host | Cached DNS response; TTL too long; no cache invalidation; systemd-resolved caching too aggressively | Intermittent failures; connects to already-deleted service | Junior Dev |
| **Resolv.conf misconfiguration** | All DNS lookups fail; "Temporary failure in name resolution" | cloud-init writes invalid resolv.conf; dhcp doesn't configure; no nameserver entry | Complete inability to resolve names; cascading failures | Junior Dev |
| **DNS server unreachable** | "Connection timed out" on all name lookups; DNS traffic blocked | Firewall rule blocks UDP:53; DNS server VM crashed; network latency too high | Complete DNS blackout; service cannot start; cascading failures | Junior Dev / Mid-level SRE |
| **Reverse DNS failures** | SSH reverse lookup hangs; logging becomes slow; syslog lookup timeouts | rdns server slow or unreachable; PTR records missing; rdns timeouts add to latency | Performance degradation; logging delays; difficult troubleshooting | Junior Dev |

---

## 3. Storage and Filesystem Problems

### 3.1 Filesystem Integrity Issues

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Filesystem corruption** | "filesystem has errors" on fsck; files disappear; data unreadable; fsck can't repair | Unclean shutdown; power loss simulation; memory corruption; overlayfs bug; concurrent writes | Data loss; entire VM unusable; recovery takes hours | Engineering Manager |
| **Inode exhaustion** | "No space left on device" even with free blocks; cannot create files | Temporary files not cleaned; empty files in loop; cache bloat; overlayfs creates inodes unnecessarily | Service stops writing; logs fill up; cascade of failures | Mid-level SRE |
| **Ext4 journal corruption** | Mount hangs during recovery; "error remounting filesystem read-only"; repeated fscks fail | Unclean unmount; corrupted journal blocks; superblock backup invalid | VM won't boot; manual recovery required | Senior SRE |
| **Overlayfs layer inconsistency** | Lower layer modified while mounted; whiteout files conflict; files appear and disappear | Concurrent modification of rootfs.ext4 during VM running; backup layer corrupted | Unpredictable behavior; files phantom-deleted; security issues | Senior SRE |
| **Copy-on-write space leak** | Overlayfs upper layer grows unexpectedly; "No space left on device" after small writes | Every write copies entire layer; no deduplication; overlay created on wrong device | Disk fills on writes; cannot persist data; VM crashes | Mid-level SRE |

### 3.2 Disk Space Issues

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Disk completely full (95%+)** | "no space left on device"; application crashes; cannot write logs; deployment fails | Log files grew; temporary files not cleaned; cache never evicted; large files in /tmp | Service becomes non-functional; cascading failures; difficult to troubleshoot | Junior Dev |
| **Hidden space consumer** | `df` shows 85%, but `du -sh /` only shows 30%; "ghost" space; mysterious disk usage | Deleted files still held open; large temp files; `.onfire-diskfill` fault file (in testing) | Disk fills unpredictably; cannot clean up; need VM restart to detect | Junior Dev / Mid-level SRE |
| **Disk I/O exhaustion** | High disk utilization; service becomes slow; swap heavily used | Too many concurrent disk writes; database doing full table scans; logs writing too fast; kernel buffering | Latency spikes; timeouts; cascading failures; overall slowdown | Mid-level SRE |
| **Swap partition exhausted** | Application gets OOM killed; swap thrashing; extremely slow system | Memory pressure causes swap overflow; no swap limits; memory leak; too many processes | Service crashes; VM becomes unusable; data corruption possible | Mid-level SRE |
| **Filesystem quota exceeded** | Writes fail; quota enforcement kicks in; unfair resource allocation | Per-user quota hit; large user writes; quota daemon crashes | Service degradation; some users blocked; incomplete writes | Junior Dev |

### 3.3 Mount & Permission Issues

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Permission denied on write** | "Permission denied" on file operations; cannot write logs; service crashes | Cloud-init creates files with wrong owner; rootfs has incorrect permissions; umask too restrictive | Service unusable; cascading failures | Junior Dev |
| **Read-only filesystem** | "Read-only file system" errors; cannot write; VM goes into read-only mode | ext4 detects corruption; journal replay failed; filesystem force-mounted as RO; hardware error | All writes blocked; VM partially functional; recovery needed | Mid-level SRE |
| **Stale NFS mounts** | Hangs on NFS operations; timeout waiting for NFS server; NFSv3 "No space left on device" | NFS server unreachable; network partition; NFS lock timeout; TCP connection stale | VM can hang indefinitely; timeout waits; cascading failures | Senior SRE |
| **Mount namespace pollution** | Orphaned mounts visible; mount leakage between VMs; mounts not isolated | Mount namespace not properly created; cloud-init mounts leak; shared mounts | VMs can see each other's filesystems; security issue; isolation breach | Engineering Manager |

---

## 4. Resource Exhaustion Scenarios

### 4.1 Memory Exhaustion

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Memory leak (simulated or real)** | Memory usage creeps up; `free` shows decreasing available; service slowdown then crash | Application memory leak; memory pressure increases; no GC; buffer bloat; cache unbounded | OOM killer activates; service crashes; cascading failures | Mid-level SRE |
| **OOM killer triggers** | Service gets SIGKILL; "Killed" in dmesg; process disappears | Memory usage hits vm.overcommit threshold; allocations exceed limit; no swap relief | Service abruptly stops; no chance to gracefully shutdown; data loss possible | Mid-level SRE |
| **Swap thrashing** | Extreme slowdown (10x+ latency spike); CPU at 100% waiting for I/O; system becomes unusable | Swap usage high; memory pressure causing page faults; not enough RAM for working set | Service becomes non-functional; timeouts; cascading failures | Mid-level SRE |
| **Page cache explosion** | "free" shows almost zero available; reading files causes memory allocation | Large sequential file reads; no readahead limiting; cache not being evicted; cgroup pressure | Memory starvation; cascading failures; OOM possible | Mid-level SRE |
| **Container memory limits exceeded** | Process killed even with free physical RAM; cgroup OOM | cgroup memory.limit_in_bytes set too low; memory accounting includes cache; limit miscalculation | Service crashes; unpredictable failures; restart loops | Junior Dev / Mid-level SRE |

### 4.2 CPU Saturation

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **CPU load at 100%** | All services slow; timeouts; p99 latency spikes; runqueue wait time high | CPU-bound workload (normal or fault injection); no throttling; too many tasks for cores | Service degradation; timeout cascades; SLA breach | Junior Dev / Mid-level SRE |
| **CPU throttling engaged** | Cgroup cpu quota exhausted; random task delays; inconsistent latency | Kernel enforces cpu.cfs_quota_us; tasks in same cgroup starve each other; unfair scheduling | Intermittent performance; hard to debug; SLA violations | Mid-level SRE |
| **Context switch overhead** | `vmstat cs` shows 100k+ context switches/sec; CPU time not in user/kernel; system time high | Too many threads/processes; cgroup contention; kernel scheduling overhead | Throughput degradation; cache miss increase; performance collapse | Mid-level SRE |
| **Runqueue starvation** | Load average high but no blocking I/O; tasks waiting for CPU slot; latency spikes | More runnable tasks than CPUs; no priority; fair scheduler under pressure | Latency unacceptable; some tasks starved; SLA breaches | Mid-level SRE |

### 4.3 File Descriptor & Connection Pool Exhaustion

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **File descriptor limit hit** | "Too many open files" errors; new connections rejected; service crashes | Application opens files without closing; no FD pooling; ulimit too low; descriptor leak | Service completely broken; cascading failures | Junior Dev / Mid-level SRE |
| **TCP connection pool exhausted** | "Connection refused"; timeout on connect; "too many connections" error; TCP port exhaustion | Connections not returned to pool; keepalive not working; connection leak; too many TIME_WAIT | Service cannot connect to dependencies; cascading failures | Mid-level SRE |
| **Database connection pool exhaustion** | "FATAL: remaining connection slots reserved for non-replication superuser" (PostgreSQL) | Connection leak in application; idle connections held; max_connections set too low | Service cannot talk to DB; cascading failures; entire tier offline | Mid-level SRE / Senior SRE |
| **Ephemeral port exhaustion** | TCP "no ephemeral ports available"; cannot establish new connections | Too many connections in TIME_WAIT; keepalive not enabled; too many retries | Service cannot reach external services; cascading failures | Mid-level SRE |
| **Thread pool exhaustion** | All worker threads busy; queue backs up; requests timeout; service stops responding | No thread limit; unbounded queue; blocking operations; no fast-fail mechanism | Service unresponsive; cascading failures; timeouts | Mid-level SRE |

---

## 5. Concurrency and Race Condition Bugs

### 5.1 Synchronization Issues

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Mutex deadlock** | Service hangs; goroutines blocked forever; no progress; watchdog timeout | Circular lock ordering; lock held during blocking operation; lock not released on error | Service completely hung; requires restart; cascading failures | Senior SRE |
| **Use-after-free** | Segfault; memory corruption; random crashes; Valgrind detects invalid access | Pointer freed but still used; dangling reference; VM ID reused before state cleaned up | Unpredictable crashes; data corruption; security issue | Senior SRE |
| **Race on shared state** | Intermittent failures; non-deterministic crashes; "sometimes works" symptoms | Concurrent reads/writes without synchronization; VM ID allocation not atomic; fleet state modified concurrently | Flaky tests; hard-to-debug production issues; data inconsistency | Mid-level SRE |
| **TOCTOU (Time-of-check-time-of-use)** | File disappears between check and use; directory renamed; symlink changed | Gap between checking existence and accessing; concurrent deletion; symlink target changed | Service crashes; data loss; security issue | Mid-level SRE |
| **Goroutine leak** | Memory usage grows over time; goroutines accumulate; `runtime.NumGoroutine()` increases | Goroutine doesn't exit; channel not closed; context not cancelled; task completion not awaited | Memory leak; eventual OOM; service degradation | Mid-level SRE |

### 5.2 Synchronization Primitive Misuse

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Channel double-close panic** | "send on closed channel" panic; fatal crash; unrecoverable | Multiple routines try to close channel; no cleanup synchronization; signal broadcast mishandled | Service crash; no graceful degradation; cascading failure | Junior Dev / Mid-level SRE |
| **Mutex used incorrectly** | Deadlock or data corruption; inconsistent state reads; lost updates | Forgetting to unlock; unlocking wrong mutex; holding lock across blocking operation | Intermittent failures; data inconsistency; cascading errors | Junior Dev |
| **Condition variable lost signal** | Goroutine waits forever; no wakeup; task never starts; hang | Signal sent before waiter blocked; condition checked outside lock; predicate changes | Service hangs; requires restart | Junior Dev / Mid-level SRE |
| **Atomic operation not enough** | Non-atomic multi-step operation appears atomic but isn't | Operations like increment not using atomic; compound operation needs lock but uses CAS | Data corruption; lost updates; race conditions | Mid-level SRE |

### 5.3 Ordering & Initialization Issues

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Double initialization** | Resource allocated twice; memory leak; conflict; port already in use | Init function runs twice; no idempotency; no guard; concurrent startup | Service startup fails; inconsistent state | Junior Dev |
| **Uninitialized state used** | Nil pointer dereference; garbage values; undefined behavior | Variable used before initialization; race on init; nil check missing | Unpredictable crashes; hard to debug | Junior Dev |
| **Shutdown before startup completes** | Service stops before becoming ready; state inconsistency; partial cleanup | Signal received during init; no ready check before accepting requests; race to completion | Service crashes; incomplete initialization | Mid-level SRE |
| **Event ordering violation** | Service performs step B before step A; prerequisites not met; cascading failure | Goroutines started without ordering; no dependency tracking; async code runs out of order | Non-deterministic failures; data inconsistency | Mid-level SRE |

---

## 6. Dependency Failures (External Tools, System Packages)

### 6.1 Missing or Broken System Dependencies

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Required binary not found** | "command not found"; fault injection fails; stress-ng/tc not available | Package not installed in rootfs; binary removed; PATH doesn't include binary | Feature completely broken; graceful fallback may exist but not tested | Junior Dev |
| **Library version mismatch** | "undefined reference" linker error; runtime load fails; "version 'GLIBC_2.XX' not found" | Shared library upgrade; ABI incompatibility; libc version skew; wrong .so symlink | Service crashes at startup; immediate failure; cascading failure | Mid-level SRE |
| **Kernel module missing** | Operations fail; "No such device or address"; feature not available; capability missing | Module not loaded; compilation missing; blacklist active; depmod stale | Feature unavailable; service degraded; workaround needed | Junior Dev / Mid-level SRE |
| **Init system misconfiguration** | systemd units fail to start; dependencies unmet; ExecStart path wrong; permission denied | Invalid unit file; Requires/After directives wrong; chroot/user mismatch; executable missing | Services don't start; cascading failures; manual intervention needed | Junior Dev |
| **Package manager database corruption** | "database is locked"; package install fails; dpkg state inconsistent | Interrupted install/upgrade; file system damage; lock file stale; concurrent apt | Cannot install packages; cannot update; trapped state | Mid-level SRE |

### 6.2 Tool Invocation Failures

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **tc (traffic control) command fails** | "RTNETLINK answers: No such device"; network fault injection doesn't work; netem not available | iproute2 not installed; network module missing; device doesn't exist; insufficient privileges | Network faults cannot be injected; testing incomplete; workaround needed | Junior Dev |
| **systemctl operation times out** | "systemctl start service" hangs; timeout waiting; service doesn't actually start | Service stuck in starting state; ExecStart blocked; dependency never met; deadlock in init | Service unavailable; manual stop/start needed; cluster impact | Mid-level SRE |
| **Firecracker binary not executable** | Permission denied; exec format error; architecture mismatch | Binary removed; wrong architecture compiled; chroot issues; SELinux denying execution | VMs cannot launch; fleet stays empty; service blocked | Junior Dev / Mid-level SRE |
| **Cloud-init script error** | User-data script fails; incomplete provisioning; service not started; configuration skipped | Syntax error in script; missing dependency; permission denied on command; bad shebang | VM provisioned incompletely; service not running; must restart VM | Junior Dev |

---

## 7. API and Service Degradation

### 7.1 HTTP/REST Service Issues

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Connection refused** | "Connection refused" immediately; port not listening; firewall blocking | Service not running; port in TIME_WAIT; iptables rule blocking; service crashed | Service completely unavailable; cascading failures | Junior Dev / Mid-level SRE |
| **Slow response time / timeouts** | p99 latency > 10s; "upstream timed out"; client timeout before response | Overload; slow database query; lock contention; memory pressure; external dependency slow | SLA breaches; user-facing degradation; cascading failures | Junior Dev / Mid-level SRE |
| **High error rate** | 5xx errors returning frequently; error logs spammed; partial availability | Unhandled exception; segfault loop; resource exhaustion; dependency failure | Service unreliable; data loss possible; SLA breach | Mid-level SRE |
| **Connection pool saturation** | "too many connections"; new requests rejected; backlog grows | Idle connections held; connection leak; no keepalive; pool size too small | Service stops accepting requests; queue overflows; cascading failures | Mid-level SRE |
| **Keep-alive disabled** | Connection churn; rapid "Connection: close"; port exhaustion; ephemeral port starvation | Header misconfiguration; client doesn't support; proxy strips keep-alive; connection recycling | Performance degradation; port exhaustion; slow scaling | Mid-level SRE |

### 7.2 Load Balancing Issues

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Uneven load distribution** | One backend at 90%, another at 10%; skewed latency; poor resource utilization | Round-robin broken; sticky sessions too sticky; health check ignores load; priority weights wrong | Poor performance; resource waste; some nodes overloaded | Junior Dev / Mid-level SRE |
| **Backend marked down incorrectly** | Healthy backend removed from pool; false positive health check; service degraded | Health check timeout too low; intermittent network; backend slow on startup; wrong check logic | Service degradation; reduced capacity; SLA impact | Junior Dev / Mid-level SRE |
| **No graceful drain on removal** | Requests drop mid-stream; connections forcefully closed; incomplete responses | No connection draining; immediate removal; no graceful shutdown window; active requests ignored | Data loss; user-visible errors; incomplete operations | Mid-level SRE |
| **Load balancer single point of failure** | LB crashes; all traffic lost; no failover; blackout | No HA configuration; failover not tested; active-active not working; VIP failover slow | Complete service blackout; massive SLA breach | Engineering Manager |

### 7.3 Rate Limiting & Throttling Failures

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Rate limiter not working** | Requests that should be rejected go through; no backpressure; DOS possible | Rate limit logic wrong; atomic increment missing; shared state race condition; bypass path exists | Service can be DOS'd; overload possible; SLA violations | Mid-level SRE |
| **Rate limiter too aggressive** | Legitimate traffic rejected; legitimate clients throttled; service appears down | Rate limit set too low; no burst allowance; shared pool with batch jobs; miscalculation | Service degradation; user complaints; SLA breaches | Junior Dev |
| **Backpressure not honored** | Requests queue indefinitely; memory fills; GC pause; no rejection | No max queue size; no timeout on enqueue; queue not drained fast enough | Memory exhaustion; cascading failures; eventual crash | Mid-level SRE |

---

## 8. State Management Issues

### 8.1 Data Consistency Failures

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Database transaction incomplete** | Partial write; data inconsistency; referential integrity broken; orphaned rows | Rollback not triggered; error during transaction; connection dropped mid-transaction | Data corruption; reports show wrong numbers; cascading failures | Engineering Manager |
| **Database deadlock** | "Deadlock detected"; transaction fails; retry loop spins; service slows | Circular dependency; locks held in different order; transaction duration too long | Service slows; retries consume resources; cascading failures | Mid-level SRE |
| **Stale read or write** | Service reads outdated data; makes decision on old state; writes don't persist | No transaction isolation; read before write completes; replication lag; cache inconsistency | Business logic broken; reports incorrect; cascading failures | Mid-level SRE |
| **Concurrent modification conflict** | Two updates conflict; last write wins but earlier one lost; merge conflict | Optimistic locking not used; no version field; race on update; conflicting merges | Data loss; inconsistency; reports wrong | Mid-level SRE |
| **Primary/replica out of sync** | Replica lags significantly; failover loses data; reads from replica return stale data | Replication lag; replica crash during replay; network partition; too many writes | Cascading failures; potential data loss on failover | Senior SRE / Eng Manager |

### 8.2 State Corruption

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Metadata corruption** | Header corruption; file size wrong; allocation bitmap corrupted; index invalid | Bit flip from cosmic ray; incomplete write; power loss; concurrent modification | Data loss; inability to read data; filesystem corruption | Engineering Manager |
| **In-memory state diverges from persistent state** | Cache says X, DB says Y; inconsistency on reload; restart changes behavior | Cache invalidation bug; write doesn't persist; cache not invalidated on update; async update doesn't complete | Hard-to-debug issues; restart fixes problem; data inconsistency | Mid-level SRE |
| **Log file corruption** | Logs unreadable; rotation fails; log parser crashes; audit trail lost | Disk corruption; concurrent write; incomplete write; charset encoding error | Cannot troubleshoot; audit trail lost; compliance issue | Senior SRE |
| **State machine invalid transition** | Service in impossible state; state diagram violated; invariant broken | Logic error in state machine; missing transition; concurrent state change | Service deadlocked or broken; manual recovery needed | Senior SRE |

### 8.3 Fleet/VM State Tracking

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **API state inconsistent with reality** | API says fleet running, but VMs are dead; API says stopped, VMs still running; commands fail | Status polling failed; database update lost; process crashed but status not updated; race condition | Manual cleanup needed; commands fail; user confusion | Mid-level SRE |
| **Zombie fleet entries** | Fleet in database but all VMs deleted; orphaned reference; cannot delete | Partial deletion; cleanup error; no cascade delete; foreign key constraint | Database bloat; confusion; cleanup needed | Junior Dev / Mid-level SRE |
| **Scenario state lost on crash** | Scenario start time forgotten; score lost; progress reset; objectives cleared | API crash; database not persisted; in-memory only; no checkpointing | Training session lost; user frustrated; must restart | Junior Dev |

---

## 9. Configuration and Validation Failures

### 9.1 Configuration Format Issues

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **YAML parsing error** | "invalid YAML"; scenario file rejected; unable to parse; cryptic error message | Indentation wrong (tabs vs spaces); unquoted special chars; missing colon; duplicate key | Scenario cannot be loaded; must fix manually; SRE confusion | Junior Dev |
| **Invalid YAML structure** | Fields missing; object expected but got string; type mismatch; schema validation fails | Missing required field; wrong data type; unknown fault type; array expected but got scalar | Scenario loads but fails on use; confusing error message | Junior Dev |
| **Default value not applied** | Field not specified but should have default; undefined behavior; null pointer dereference | No default handling in code; required field missing; implicit assumption wrong | Service uses garbage value; unpredictable behavior | Junior Dev / Mid-level SRE |
| **Configuration drift** | Running config differs from file; manual edits lost; changes not persisted | Edit without saving; process cache; configuration reload failed; concurrent edit | Inconsistency; restart loses changes; confusion | Junior Dev |

### 9.2 Validation Failures

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Invalid fault parameters** | Fault starts but behaves unexpectedly; parameters ignored; silent failure | No validation; bad parameter accepted; wrong type; out of range | Fault doesn't work as intended; testing unreliable; debugging difficult | Junior Dev |
| **Out-of-range values** | Load: 150% (impossible); memory: -500MB; negative disk size | No bounds checking; validation missing; type conversion issue; user input not sanitized | Undefined behavior; service crash or hang; nonsensical operation | Junior Dev / Mid-level SRE |
| **Incompatible combination** | cpu load 90% but only 0.1 CPUs; memory leak rate faster than total RAM; all contradictory | No cross-field validation; constraints not checked; mutually exclusive options allowed | Fault cannot execute; silent failure; confusing behavior | Junior Dev |
| **Malformed scenario** | Architecture: "microservises" (typo); difficulty: "ultra-hard" (undefined); fault type: "cpu-rage" | No enum validation; typos not caught; unknown values silently ignored | Scenario behaves unexpectedly; fault not injected; difficult debugging | Junior Dev |

### 9.3 Secrets & Security Configuration

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Hardcoded credentials exposed** | SSH keys in cloud-init; passwords in logs; secrets in version control; readable by all | Security misconfiguration; no secrets management; keys generated insecurely | Security breach; unauthorized access; compliance violation | Engineering Manager |
| **Overly permissive file permissions** | Config file world-readable; secret file writable by all; umask too permissive | File created with mode 0666; no permission restrictions; inheritance from parent | Security breach; secrets exposed; unauthorized modification | Engineering Manager |
| **No credential rotation** | Same SSH key used forever; API token never refreshed; secret password written in stone | No rotation mechanism; manual process skipped; emergency access becomes permanent | Security risk; breach impact larger; compliance issue | Senior SRE / Eng Manager |

---

## 10. Integration Failures

### 10.1 Cross-Service Communication Failures

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Service discovery failure** | Cannot resolve service name; wrong IP returned; stale entries; no service found | DNS outdated; service unregistered; cache stale; wrong zone; load balancer down | Cascading failures; service unable to find dependencies; blackout | Mid-level SRE |
| **API version mismatch** | Old client talks to new server; missing fields; incompatible protocol; unexpected response format | No API versioning; breaking change; client not updated; server upgrade without compatibility | Service fails; data corruption; cascading failures | Mid-level SRE |
| **Timeout on external service** | "Connection timed out"; response never arrives; request hangs | External service slow; network latency; firewall blocking; TCP window stalled | Cascade of timeouts; service degradation; SLA breach | Junior Dev / Mid-level SRE |
| **Circuit breaker not working** | All requests fail; cascading failure; no fast-fail; queue overflows | Circuit breaker logic broken; never opens; open circuit times out; state corrupted | Service degradation; resource exhaustion; cascading failures | Mid-level SRE |
| **Partial dependency failure** | Some database nodes down; some API instances unreachable; degraded mode expected but not coded | No graceful degradation; all-or-nothing logic; no fallback; retry doesn't help | Service fails completely instead of degrading; unnecessary SLA breach | Mid-level SRE |

### 10.2 Webhook & Event Integration Issues

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Webhook not delivered** | Notification never received; event lost; action not triggered; user unaware | Network timeout; server unreachable; firewall blocking; delivery endpoint wrong | Alerts don't trigger; user doesn't respond; automated action doesn't execute | Mid-level SRE |
| **Webhook retry storm** | Endpoint gets hammered by retries; overload from retry backoff; exponential growth | Failing endpoint; no exponential backoff; retry forever logic; no max retries | Cascading failure; amplified load; DDoS-like | Senior SRE |
| **Duplicate event delivery** | Same event fires twice; billing charges double; action runs twice | At-least-once semantics without deduplication; retry without idempotency; race condition | Incorrect state; billing issues; duplicated actions | Mid-level SRE |
| **Event ordering violation** | Events arrive out of order; state machine sees invalid transition; dependent events mis-ordered | Network reordering; async delivery; consumer processes out of order | State corruption; business logic broken; cascading failures | Mid-level SRE |

---

## 11. Hardware and Kernel Issues

### 11.1 Host System Problems

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **KVM module not loaded** | Cannot access /dev/kvm; "Operation not permitted"; VMs won't start | Kernel module disabled; not compiled; blacklisted; requires reboot; insufficient privileges | VMs completely non-functional; entire platform useless | Junior Dev / Mid-level SRE |
| **Insufficient CPU cores** | VMs become very slow; CPU quota enforcement; starved runqueue | Host oversubscribed; too many VMs for physical cores; no CPU limiting; scheduler contention | Service degradation; performance unacceptable; scaling broken | Mid-level SRE |
| **Out of memory on host** | Swap thrashing; OOM killer activates; VMs killed; system becomes unresponsive | All VMs hungry for memory; no limit enforcement; memory pressure crisis; cascading failures | Host becomes unusable; VMs killed; cascading failures | Senior SRE / Eng Manager |
| **Disk full on host** | VMs cannot write rootfs; "No space left on device"; VM storage operations fail | /tmp filled; logs grew; large image files not cleaned up; disk quota exceeded | VMs cannot function; new deployments fail; scaling blocked | Mid-level SRE |
| **CPU throttling / TDP limit** | Service suddenly slow; CPU frequency reduced; turbo disabled; power limit hit | Thermal throttling; power budget exceeded; BIOS power limit; battery conservation mode | Service degradation; latency spikes; unpredictable slowdown | Mid-level SRE |

### 11.2 Kernel Scheduling Issues

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Kernel deadlock** | System hangs completely; no progress; even `magic sysrq` doesn't work; hardware watchdog fires | Circular dependency in kernel code; livelock; spinlock never released | Entire host unusable; requires hard reboot; all VMs lost | Engineering Manager |
| **Kernel memory leak** | Kernel memory never freed; slab growing; page tables leaked; swap pressure; OOM eventual | Memory management bug; drivers not cleaning up; cgroup memory leak | Long-term degradation; eventual OOM; services crash | Senior SRE |
| **Too many open files on host** | "Too many open files" on host side; system limit hit; cannot open new files | Descriptor leak in host kernel; services accumulate FDs; /proc/sys/fs/file-max too low | Cannot function; need reboot; FD space exhaustion | Senior SRE |
| **Kernel timeout bug** | Operations timeout mysteriously; reproducible hang; scheduler stall; RCU stall | Kernel bug in specific scenario; interrupt handling bug; scheduler bug; synchronization issue | Unpredictable hangs; hard to troubleshoot; upgrade kernel | Senior SRE |

### 11.3 Hardware-Level Failures

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **CPU instruction not available** | Illegal instruction fault; SIGILL; crash when executing instruction set extension | CPU doesn't support instruction; compiled with wrong flags; live migration between different CPU models | Crash on certain operations; cascading failure; platform incompatible | Engineering Manager |
| **Disk I/O errors** | ECC errors; "read: input/output error"; bad blocks; unreliable reads/writes | Disk wear; bad sectors; controller failure; data corruption from bit flip | Data loss; file system corruption; VM unusable | Engineering Manager |
| **Memory bit flip (ECC failure)** | Silent data corruption; wrong results; data doesn't match checksum; Valgrind detects errors | Single event upset; radiation; memory module failing; ECC not enabled | Unpredictable corruption; hard to detect; data integrity compromised | Engineering Manager |
| **Network interface failure** | Dropped packets; intermittent connectivity; "No such device" on TAP device | NIC hardware failure; driver crash; PHY timeout; cable issue | Service disruption; intermittent failures; cascading failures | Mid-level SRE |

---

## 12. Security and Permission Issues

### 12.1 Access Control Failures

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **Running with excessive privileges** | Service runs as root; can modify any file; security boundary violated | No privilege separation; no user account; default configuration; lazy approach | Security breach; lateral movement; privilege escalation possible | Engineering Manager |
| **Setuid bit exploited** | Privilege escalation; attacker gains root; boundary violated; sandbox escape | Setuid binary vulnerable; buffer overflow; symlink race; TOCTOUJ vulnerability | Complete system compromise; attacker has root; all data accessible | Engineering Manager |
| **File permissions too permissive** | World-readable secrets; world-writable binaries; group readable private data | umask 0; chmod 0777; default permissions; no restriction | Security breach; data exposure; code injection possible | Engineering Manager |
| **chroot escape** | Attacker escapes chroot; accesses parent filesystem; chroot jailbreak | Hardlink attack; shared inode; VFS dentry cache; race condition in chroot | Sandbox broken; attacker has host access; full compromise | Engineering Manager |
| **Capability not dropped** | Service runs with CAP_SYS_ADMIN; can do anything; no privilege limiting | No capabilities.drop; default to all; forgot to restrict; no least privilege | Security risk; lateral movement; privilege escalation | Engineering Manager |

### 12.2 Authentication & Authorization Failures

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **No authentication on API** | Anyone can call /api endpoints; no token check; no credentials required; public API | Authentication not implemented; disabled in dev mode; default insecurity | Unauthorized access; data exposure; service manipulation | Engineering Manager |
| **Session hijacking** | Attacker steals session token; impersonates user; accesses private data | No HTTPS; token in URL; no CSRF token; predictable token | Account takeover; data exposure; unauthorized actions | Engineering Manager |
| **Password weak or default** | Accounts use default credentials; password == username; password in logs; weak hash | Default password never changed; no password policy; plaintext storage; weak algorithm | Account compromise; unauthorized access; data exposure | Engineering Manager |
| **RBAC not enforced** | Regular user can perform admin action; authorization check missing; role not validated | Missing permission check; role-based logic not implemented; bypass path exists | Unauthorized access; data corruption; policy violation | Engineering Manager |

### 12.3 Cryptography Failures

| Failure Mode | Symptoms | Root Cause | Impact | Typical Severity |
|---|---|---|---|---|
| **No HTTPS / encryption in transit** | Credentials sent over HTTP; network sniffing possible; man-in-the-middle | SSL/TLS not enforced; self-signed cert not validated; protocol downgrade; cert expired | Data exposure; credential theft; session hijacking | Engineering Manager |
| **Weak cipher suite** | DES or MD5 used; algorithm known broken; decryption feasible; brute force possible | Old TLS version; weak algorithms; default insecure config; no cipher restriction | Data exposure; brute-force attacks possible; compliance failure | Engineering Manager |
| **Random number generator weak** | Token collisions; predictable IDs; PRNG seed reused; insufficient entropy | Using rand instead of crypto/rand; seed from time; PRNG state leaked; clock-based seed | Collision attacks; token prediction; security issue | Engineering Manager |
| **Secret in memory unprotected** | Memory dump reveals password; swapped to disk; debugger can read; coredump captures secret | No memory pinning; secrets on heap; no zeroing after use; coredump enabled | Secrets exposed; credential compromise; account takeover | Engineering Manager |

---

## Summary: Severity Level Guide

### **Junior Developer / Support Engineer**
- Straightforward issues with clear root causes
- Single-component failures
- Configuration mistakes, typos, missing parameters
- DNS, firewall, basic networking issues
- Simple service startup failures
- Well-documented troubleshooting paths

**Typical examples**: disk full, missing package, invalid YAML, DNS hijack, permission denied

### **Mid-Level SRE**
- Multi-component failures requiring root cause analysis
- Concurrency bugs, race conditions, synchronization issues
- Resource exhaustion scenarios (memory leaks, connection pools)
- Network architecture issues, load balancer misconfiguration
- Database deadlocks, replication lag
- Performance degradation, latency spikes
- Cascading failures requiring intervention at multiple layers
- Requires on-call escalation, incident response coordination

**Typical examples**: connection pool exhaustion, cascading latency, CPU saturation, TAP device issues, concurrent modification bugs

### **Senior SRE / Architecture**
- Complex distributed system failures
- Kernel-level issues, hardware problems
- Filesystem corruption, data consistency violations
- Deadlocks, undefined behavior, memory corruption
- Security vulnerabilities, privilege escalation
- Complex synchronization bugs, use-after-free
- Architecture changes, design flaws
- Cross-regional or multi-cluster issues
- Requires deep expertise, post-mortem analysis

**Typical examples**: kernel deadlock, filesystem corruption, memory bit flips, race condition in ID allocation, state machine violations

### **Engineering Manager / Platform Owner**
- Business-critical outages lasting >1 hour
- Data loss incidents, security breaches
- SLA breaches affecting paying customers
- Cascading failures across multiple services
- Requires executive communication
- Impacts company reputation or revenue
- Requires architecture redesign
- Post-incident review and systemic changes

**Typical examples**: complete DNS outage, database corruption, security breach, host kernel deadlock, multi-region failover required

---

## Incident Response Reference

| Symptom | Diagnosis Approach | Quick Mitigation | Escalation Threshold |
|---|---|---|---|
| **Service responds with 500 errors** | Check logs, dependency health, resource usage (CPU/mem/disk), database connectivity | Restart service, drain traffic, enable circuit breaker | >30% error rate OR customer-facing AND unresolved >5m |
| **Service times out** | Check latency metrics, database query log, network latency, CPU saturation, lock contention | Reduce load, optimize query, increase timeout, scale horizontally | >p99 latency 2s OR SLA breached |
| **Memory usage climbing** | Memory leak detection, GC logs, goroutine count, heap dump analysis | Restart service, disable caching, investigate goroutine leaks | Approaching 80% of limit |
| **Disk full** | Identify large files, log rotation, temp file cleanup, du analysis | Delete temp files, rotate logs, clean cache | >90% usage AND critical service impacted |
| **Network connectivity issues** | Check IP connectivity, DNS resolution, firewall rules, routing table | Restart network interfaces, check iptables, verify TAP devices | Any VM can't reach gateway OR complete isolation |
| **Cascading failures** | Identify entry point, check upstream service health, dependency chain | Circuit breaker, traffic drain, service restart in order | More than 2 tiers affected |

---

## Testing Recommendations for Firecracker Platform

To ensure comprehensive failure resilience, regularly test:

1. **VM lifecycle**: Launch/kill cycles under load; verify cleanup
2. **Network isolation**: TAP device failures, IP collisions, network partition simulation
3. **Resource constraints**: Memory exhaustion, disk full, CPU saturation
4. **Concurrency**: Rapid fleet creation/deletion, concurrent scenario execution
5. **Dependency failures**: Missing tools, broken systemd units, malformed cloud-init
6. **State consistency**: Restart scenarios, API server restart, database inconsistency
7. **Security**: Unauthorized access attempts, privilege escalation, secret exposure
8. **Cascading failures**: Trigger multiple faults simultaneously; verify graceful degradation
9. **Chaos scenarios**: Random VM kills, network latency injection, cross-service failures
10. **Recovery**: Test runbooks for each failure mode; measure MTTR

