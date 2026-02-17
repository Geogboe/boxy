package resourcespec

import "testing"

func TestNormalize_SortsAndDedupesSets(t *testing.T) {
	t.Parallel()

	s := Normalize(Spec{
		Base:     BaseSpec{Kind: "docker.image", Ref: "ubuntu:latest"},
		Packages: []string{" curl", "python3", "curl", ""},
		Groups:   []string{"dev", " dev ", "ops"},
	})

	if want, got := 2, len(s.Packages); want != got {
		t.Fatalf("packages len=%d, want %d (%v)", got, want, s.Packages)
	}
	if s.Packages[0] != "curl" || s.Packages[1] != "python3" {
		t.Fatalf("packages=%v, want [curl python3]", s.Packages)
	}
	if s.Groups[0] != "dev" || s.Groups[1] != "ops" {
		t.Fatalf("groups=%v, want [dev ops]", s.Groups)
	}
}

func TestDigest_StableAcrossOrdering(t *testing.T) {
	t.Parallel()

	a := Spec{
		Base:     BaseSpec{Kind: "hyperv.vhdx", Ref: "win2022"},
		Packages: []string{"b", "a"},
		Labels:   map[string]string{"z": "1", "a": "2"},
	}
	b := Spec{
		Base:     BaseSpec{Kind: "hyperv.vhdx", Ref: "win2022"},
		Packages: []string{"a", "b"},
		Labels:   map[string]string{"a": "2", "z": "1"},
	}

	da, err := Digest(a)
	if err != nil {
		t.Fatalf("Digest(a): %v", err)
	}
	db, err := Digest(b)
	if err != nil {
		t.Fatalf("Digest(b): %v", err)
	}
	if da != db {
		t.Fatalf("digest mismatch: %s != %s", da, db)
	}
}

func TestPlanUpgrade_Packages_AddOnly(t *testing.T) {
	t.Parallel()

	from := Spec{Base: BaseSpec{Kind: "k", Ref: "r"}, Packages: []string{"curl"}}
	to := Spec{Base: BaseSpec{Kind: "k", Ref: "r"}, Packages: []string{"curl", "python3"}}

	ops, err := PlanUpgrade(from, to)
	if err != nil {
		t.Fatalf("PlanUpgrade: %v", err)
	}
	found := false
	for _, op := range ops {
		if p, ok := op.(InstallPackage); ok && p.Name == "python3" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected InstallPackage python3, got ops=%T %v", ops, ops)
	}
}

func TestPlanUpgrade_Packages_RemovalRejected(t *testing.T) {
	t.Parallel()

	from := Spec{Base: BaseSpec{Kind: "k", Ref: "r"}, Packages: []string{"curl", "python3"}}
	to := Spec{Base: BaseSpec{Kind: "k", Ref: "r"}, Packages: []string{"curl"}}

	if _, err := PlanUpgrade(from, to); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestPlanUpgrade_ServiceDisableRejected(t *testing.T) {
	t.Parallel()

	from := Spec{Base: BaseSpec{Kind: "k", Ref: "r"}, Services: []ServiceSpec{{Name: "ssh", State: ServiceEnabled}}}
	to := Spec{Base: BaseSpec{Kind: "k", Ref: "r"}, Services: []ServiceSpec{{Name: "ssh", State: ServiceDisabled}}}

	if _, err := PlanUpgrade(from, to); err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestPlanUpgrade_UserRemovalRejected(t *testing.T) {
	t.Parallel()

	from := Spec{Base: BaseSpec{Kind: "k", Ref: "r"}, Users: []UserSpec{{Name: "alice"}}}
	to := Spec{Base: BaseSpec{Kind: "k", Ref: "r"}}

	if _, err := PlanUpgrade(from, to); err == nil {
		t.Fatalf("expected error, got nil")
	}
}
