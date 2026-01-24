package storage

import "testing"

func TestValidatePropertiesForStorageRejectsUnsupportedType(t *testing.T) {
	type badValue struct {
		Reason string
	}

	props := map[string]interface{}{
		"bad": badValue{Reason: "nope"},
	}

	if err := validatePropertiesForStorage(props); err == nil {
		t.Fatal("expected error for unsupported property type")
	}
}

func TestValidatePropertiesForStorageAcceptsNestedValues(t *testing.T) {
	props := map[string]interface{}{
		"meta": map[string]interface{}{
			"tags": []interface{}{"a", "b", int64(3)},
			"ok":   true,
		},
	}

	if err := validatePropertiesForStorage(props); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
