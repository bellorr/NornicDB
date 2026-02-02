package auth

import (
	"context"
	"testing"

	"github.com/orneryd/nornicdb/pkg/storage"
)

func TestRoleStore_AllRoles_Create_Delete(t *testing.T) {
	ctx := context.Background()
	eng := storage.NewMemoryEngine()
	defer eng.Close()

	store := NewRoleStore(eng)
	if err := store.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	all := store.AllRoles()
	if len(all) != 3 {
		t.Errorf("expected 3 built-in roles, got %v", all)
	}

	if err := store.CreateRole(ctx, "analyst"); err != nil {
		t.Fatalf("CreateRole analyst: %v", err)
	}
	if err := store.CreateRole(ctx, "admin"); err != ErrRoleExists {
		t.Errorf("CreateRole admin should fail with ErrRoleExists, got %v", err)
	}
	all2 := store.AllRoles()
	if len(all2) != 4 {
		t.Errorf("expected 4 roles after create, got %v", all2)
	}

	if err := store.DeleteRole(ctx, "admin"); err != ErrCannotDeleteBuiltinRole {
		t.Errorf("DeleteRole admin should fail, got %v", err)
	}
	if err := store.DeleteRole(ctx, "analyst"); err != nil {
		t.Fatalf("DeleteRole analyst: %v", err)
	}
	all3 := store.AllRoles()
	if len(all3) != 3 {
		t.Errorf("expected 3 roles after delete, got %v", all3)
	}
}

func TestRoleStore_RenameRole(t *testing.T) {
	ctx := context.Background()
	eng := storage.NewMemoryEngine()
	defer eng.Close()

	store := NewRoleStore(eng)
	if err := store.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := store.CreateRole(ctx, "oldname"); err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if err := store.RenameRole(ctx, "oldname", "newname"); err != nil {
		t.Fatalf("RenameRole: %v", err)
	}
	if store.Exists("oldname") {
		t.Error("oldname should not exist after rename")
	}
	if !store.Exists("newname") {
		t.Error("newname should exist after rename")
	}
}
