# TODO

## Test Coverage Gaps

### API VM ID pool (api/server.go)
- Unit tests for `allocateVMIDs` / `releaseVMIDs`
- Verify lowest-available-first allocation
- Verify IDs are returned to the pool after scenario completes
- Concurrent allocation (two goroutines allocating simultaneously)

### Concurrent scenario integration
- Spin up two runners with overlapping lifetimes and assert they receive disjoint VM ID sets
- Verify no TAP device collision (EnsureTAP called with IDs from both sets)

### BuildVMPlan edge cases (already written, pending commit)
- `TestBuildVMPlan_WithVMIDs` — VMEntry.Index holds allocated ID, ByName/ByTier store slice positions
- `TestBuildVMPlan_WithVMIDs_ResolveTargets` — ResolveTargets returns in-bounds slice position even with non-sequential IDs
