# TODO: Firecracker VM Snapshot Support

## Goal
Enable pre-baked VM snapshots as baseline for lab exercises. Trainees spawn fully-booted instances in 28ms instead of 3-5s, each with their own writable COW copy.

Use case: "Here's a working web server, now misconfigure it" or "This VM has tools you need, solve the problem"

---

## Architecture

### Capture phase (build-time, once per snapshot)
1. Cold boot a reference VM normally via Firecracker
2. Wait for guest agent to signal "ready" (via `/api/ready` or similar)
3. Firecracker API: `pause` the VM, then `create_snapshot` (memory + device state)
4. Store snapshot tarball: `snapshot.tar.gz` (memory dump + metadata)

### Restore phase (runtime, per trainee)
1. Copy snapshot files to per-VM location using COW (nearly instant)
2. Firecracker API: `load_snapshot` + start with snapshot flag
3. VM resumes from frozen state (28ms total)
4. Agent reconnects and re-runs lightweight setup (hostname, network)

---

## Implementation Plan

### Phase 1: Core snapshot plumbing (vm/snapshot.go)

Create new file `vm/snapshot.go`:
```go
// CaptureSnapshot(ctx, cfg, outputPath) error
//   - Start VM normally via vm.New()
//   - Wait for agent /ready endpoint (poll agent HTTP)
//   - Call Firecracker pause() API
//   - Call Firecracker create_snapshot(outputPath) API
//   - Return path to snapshot tarball

// RestoreSnapshot(ctx, cfg, snapshotPath) (*Machine, error)
//   - Validate snapshot exists
//   - Copy snapshot files to per-VM location (COW)
//   - Call Firecracker with load_snapshot flag
//   - Return *Machine
```

**Key decisions:**
- Snapshot format: Firecracker's native binary (memory dump + metadata)
- Storage: Host filesystem, path passed as CLI arg
- Metadata: Store snapshot creation time, rootfs hash, agent version in separate JSON file

### Phase 2: CLI commands

#### `cmd/run.go` — Add snapshot capture
```
onfire run --rootfs rootfs.ext4 --snapshot-out snapshot.tar --snapshot-capture
```
- Start a reference VM
- Wait for readiness
- Capture snapshot
- Exit

#### `cmd/fleet.go` — Add snapshot restore
```
onfire fleet create --count 5 --snapshot snapshot.tar
```
- Per-VM: copy snapshot to `vm-{id}-snapshot`
- Per-VM: restore from snapshot + agent reconnect
- Parallel launch (same as today, but 28ms vs 3-5s per VM)

### Phase 3: API support (api/fleet.go, onfirec/main.go)

**REST API:**
- POST `/api/v1/fleets` accepts `snapshot_path` field (mutually exclusive with `rootfs`)
- Routes to `launchFleetFromSnapshot(ctx, snapshotPath, vmIDs, deps)`

**CLI client (`onfirec/main.go`):**
```
onfirec fleet create --count 5 --snapshot /path/to/snapshot.tar
```
- Sends `"snapshot_path": "/path/to/snapshot.tar"` in request body

### Phase 4: Testing

#### Unit tests (vm/snapshot_test.go)
- Mock Firecracker API; verify pause + create_snapshot called correctly
- Mock agent readiness check; simulate timeout
- Verify COW copy behavior (reflink fallback)

#### Integration tests
- Capture snapshot from a real VM (if CI has /dev/kvm)
- Restore snapshot; verify `/etc/hostname` set correctly
- Verify agent can reconnect post-restore

---

## Files to create/modify

| File | Change |
|---|---|
| `vm/snapshot.go` | **NEW**: CaptureSnapshot(), RestoreSnapshot() |
| `vm/snapshot_test.go` | **NEW**: Tests for snapshot capture and restore |
| `cmd/run.go` | Add `--snapshot-out` + `--snapshot-capture` flags; add capture logic |
| `cmd/fleet.go` | Add snapshot restore path (parallel to launchFleet) |
| `api/fleet.go` | Add `SnapshotPath` to fleetCreateRequest; route to restoreFleet() |
| `api/server.go` | Extend LaunchDeps interface with snapshot methods |
| `onfirec/main.go` | Add `--snapshot` flag to fleet create; update usage |

---

## Firecracker API calls (pseudocode)

```go
// Pause (to freeze state)
PUT /vm.pause
{}

// Snapshot (capture memory + device state)
PUT /snapshot/create
{
  "snapshot_type": "Full",
  "snapshot_path": "/path/to/snapshot/memory",
  "mem_file_path": "/path/to/snapshot/memory-dump"
}

// Load snapshot (at restore time)
PUT /snapshot/load
{
  "snapshot_path": "/path/to/snapshot/memory",
  "mem_file_path": "/path/to/snapshot/memory-dump",
  "enable_diff_snapshots": false
}
```

---

## Known constraints

1. **Per-VM customization**: After snapshot restore, hostname is stale
   - Fix: Agent runs lightweight script to update `/etc/hostname` via debugfs
   - Or: Pre-bake per-VM snapshots (defeats space savings; skip for now)

2. **Snapshot size**: ≈ MemMB per snapshot (e.g., 512MB VM → 512MB snapshot file)
   - Acceptable for 5-10 concurrent trainees
   - Discuss if scaling beyond that

3. **Snapshot versioning**: If rootfs changes, snapshots become invalid
   - Metadata file with build hash + instructions to recreate snapshot
   - CI/build system should regenerate on rootfs change

4. **Agent readiness**: Need robust way to detect when guest is ready for snapshot
   - Agent exposes `/api/ready` endpoint returning `{ "ready": true }`
   - Timeout after 30s → error

---

## Rollout phases

**Phase 1** (foundation):
- Implement vm/snapshot.go (capture + restore logic)
- Add `onfire run --snapshot-capture`
- Write tests

**Phase 2** (CLI):
- Integrate into fleet creation
- Update onfirec CLI
- Manual testing

**Phase 3** (optional polish):
- Metadata versioning + auto-regeneration
- Diff snapshots (incremental) if memory usage is concern
- Concurrent snapshot capture (for multiple pre-baked VM types)

---

## Success criteria

- ✓ `onfire run --snapshot-capture` produces valid snapshot.tar
- ✓ `onfirec fleet create --count 5 --snapshot snapshot.tar` launches 5 VMs in <500ms total
- ✓ Each VM has correct hostname + working agent
- ✓ All existing tests pass
- ✓ New snapshot tests achieve >80% coverage

---

## References

- Firecracker snapshot API: https://github.com/firecracker-microvm/firecracker/blob/master/docs/snapshotting/snapshot-support.md
- ForgeVM blog: https://dev.to/adwitiya/how-i-built-sandboxes-that-boot-in-28ms-using-firecracker-snapshots-i0k
