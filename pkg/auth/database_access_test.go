package auth

import "testing"

func TestPrivilegeDatabaseRef(t *testing.T) {
	ref := PrivilegeDatabaseRef{Name: "nornic", OwningDatabaseName: "nornic"}
	if ref.Name != "nornic" || ref.OwningDatabaseName != "nornic" {
		t.Errorf("PrivilegeDatabaseRef: got Name=%q Owning=%q", ref.Name, ref.OwningDatabaseName)
	}
}

func TestFullDatabaseAccessMode(t *testing.T) {
	if !FullDatabaseAccessMode.CanSeeDatabase("nornic") {
		t.Error("Full: CanSeeDatabase(nornic) want true")
	}
	if !FullDatabaseAccessMode.CanAccessDatabase("nornic") {
		t.Error("Full: CanAccessDatabase(nornic) want true")
	}
	if !FullDatabaseAccessMode.CanAccessDatabase("system") {
		t.Error("Full: CanAccessDatabase(system) want true")
	}
}

func TestDenyAllDatabaseAccessMode(t *testing.T) {
	if DenyAllDatabaseAccessMode.CanSeeDatabase("nornic") {
		t.Error("DenyAll: CanSeeDatabase(nornic) want false")
	}
	if DenyAllDatabaseAccessMode.CanAccessDatabase("nornic") {
		t.Error("DenyAll: CanAccessDatabase(nornic) want false")
	}
	if DenyAllDatabaseAccessMode.CanAccessDatabase("") {
		t.Error("DenyAll: CanAccessDatabase(\"\") want false")
	}
}

func TestResolvedAccess(t *testing.T) {
	ra := ResolvedAccess{Read: true, Write: false}
	if !ra.Read || ra.Write {
		t.Errorf("ResolvedAccess: got Read=%v Write=%v", ra.Read, ra.Write)
	}
}
