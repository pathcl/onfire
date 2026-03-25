package scenario_test

import (
	"testing"

	"github.com/pathcl/onfire/scenario"
)

func TestLoad_ValidScenario(t *testing.T) {
	s, err := scenario.Load("testdata/minimal.yaml")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if s.Meta.ID != "test-001" {
		t.Errorf("Meta.ID = %q, want %q", s.Meta.ID, "test-001")
	}
	if len(s.Environment.Tiers) != 1 {
		t.Errorf("len(Tiers) = %d, want 1", len(s.Environment.Tiers))
	}
	if len(s.Faults) != 1 {
		t.Errorf("len(Faults) = %d, want 1", len(s.Faults))
	}
	if len(s.Hints) != 2 {
		t.Errorf("len(Hints) = %d, want 2", len(s.Hints))
	}
	if s.Scoring.Base != 100 {
		t.Errorf("Scoring.Base = %d, want 100", s.Scoring.Base)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := scenario.Load("testdata/does-not-exist.yaml")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestLoad_DefaultScoring(t *testing.T) {
	// A scenario with no scoring block should get default scoring applied.
	s, err := scenario.Load("testdata/minimal.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if s.Scoring.TimePenaltyPerSecond == 0 {
		t.Error("expected non-zero TimePenaltyPerSecond from defaults")
	}
}

func TestBuildVMPlan_Basic(t *testing.T) {
	s, err := scenario.Load("testdata/minimal.yaml")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := scenario.BuildVMPlan(s, nil, nil)
	if err != nil {
		t.Fatalf("BuildVMPlan() error: %v", err)
	}
	if plan.TotalVMs != 1 {
		t.Errorf("TotalVMs = %d, want 1", plan.TotalVMs)
	}
	if _, ok := plan.ByName["app-0"]; !ok {
		t.Error("expected VM named app-0")
	}
}

func TestBuildVMPlan_ScaleOverride(t *testing.T) {
	s, err := scenario.Load("testdata/minimal.yaml")
	if err != nil {
		t.Fatal(err)
	}

	plan, err := scenario.BuildVMPlan(s, map[string]int{"app": 3}, nil)
	if err != nil {
		t.Fatalf("BuildVMPlan() error: %v", err)
	}
	if plan.TotalVMs != 3 {
		t.Errorf("TotalVMs = %d, want 3", plan.TotalVMs)
	}
	if len(plan.ByTier["app"]) != 3 {
		t.Errorf("ByTier[app] = %v, want 3 entries", plan.ByTier["app"])
	}
}

func TestResolveTargets_ByVM(t *testing.T) {
	s, _ := scenario.Load("testdata/minimal.yaml")
	plan, _ := scenario.BuildVMPlan(s, nil, nil)

	target := scenario.FaultTarget{VM: "app-0"}
	indices, err := scenario.ResolveTargets(target, plan)
	if err != nil {
		t.Fatalf("ResolveTargets() error: %v", err)
	}
	if len(indices) != 1 || indices[0] != 0 {
		t.Errorf("ResolveTargets() = %v, want [0]", indices)
	}
}

func TestResolveTargets_UnknownVM(t *testing.T) {
	s, _ := scenario.Load("testdata/minimal.yaml")
	plan, _ := scenario.BuildVMPlan(s, nil, nil)

	_, err := scenario.ResolveTargets(scenario.FaultTarget{VM: "web-99"}, plan)
	if err == nil {
		t.Error("expected error for unknown VM, got nil")
	}
}

// TestBuildVMPlan_WithVMIDs verifies that when globally-allocated IDs are
// provided, VMEntry.Index holds the allocated ID while ByName/ByTier hold
// slice positions so that plan.VMs[slicePos] is always a valid access.
func TestBuildVMPlan_WithVMIDs(t *testing.T) {
	s, err := scenario.Load("testdata/minimal.yaml")
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a second scenario getting VM IDs starting at 5.
	vmIDs := []int{5}
	plan, err := scenario.BuildVMPlan(s, nil, vmIDs)
	if err != nil {
		t.Fatalf("BuildVMPlan() error: %v", err)
	}

	if plan.TotalVMs != 1 {
		t.Fatalf("TotalVMs = %d, want 1", plan.TotalVMs)
	}

	// VMEntry.Index must be the allocated network ID (5), not 0.
	if plan.VMs[0].Index != 5 {
		t.Errorf("VMs[0].Index = %d, want 5", plan.VMs[0].Index)
	}

	// ByName must store the slice position (0), not the allocated ID (5).
	slicePos, ok := plan.ByName["app-0"]
	if !ok {
		t.Fatal("expected ByName[\"app-0\"] to exist")
	}
	if slicePos != 0 {
		t.Errorf("ByName[\"app-0\"] = %d (slice pos), want 0", slicePos)
	}

	// Accessing plan.VMs[slicePos] must give the VM with Index==5.
	if plan.VMs[slicePos].Index != 5 {
		t.Errorf("VMs[slicePos].Index = %d, want 5", plan.VMs[slicePos].Index)
	}
}

// TestBuildVMPlan_WithVMIDs_ResolveTargets confirms that ResolveTargets
// returns a slice position that safely indexes plan.VMs when non-sequential
// VM IDs are in use.
func TestBuildVMPlan_WithVMIDs_ResolveTargets(t *testing.T) {
	s, err := scenario.Load("testdata/minimal.yaml")
	if err != nil {
		t.Fatal(err)
	}

	vmIDs := []int{7}
	plan, err := scenario.BuildVMPlan(s, nil, vmIDs)
	if err != nil {
		t.Fatal(err)
	}

	indices, err := scenario.ResolveTargets(scenario.FaultTarget{VM: "app-0"}, plan)
	if err != nil {
		t.Fatalf("ResolveTargets() error: %v", err)
	}
	if len(indices) != 1 {
		t.Fatalf("ResolveTargets() returned %d indices, want 1", len(indices))
	}

	slicePos := indices[0]

	// slicePos must be valid for plan.VMs — this would panic if it stored 7.
	if slicePos >= len(plan.VMs) {
		t.Fatalf("ResolveTargets() returned %d which is out of bounds for plan.VMs (len=%d)", slicePos, len(plan.VMs))
	}

	// And the VM at that slice position should have the allocated network ID.
	if plan.VMs[slicePos].Index != 7 {
		t.Errorf("VMs[slicePos].Index = %d, want 7", plan.VMs[slicePos].Index)
	}
}
