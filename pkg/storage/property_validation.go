package storage

import (
	"fmt"
	"time"
)

// validatePropertiesForStorage ensures property values are gob-encodable.
// This prevents runtime serialization failures during async flush.
func validatePropertiesForStorage(properties map[string]interface{}) error {
	if len(properties) == 0 {
		return nil
	}
	for key, value := range properties {
		if err := validatePropertyValueForStorage(value); err != nil {
			return fmt.Errorf("invalid property value for key %q: %w", key, err)
		}
	}
	return nil
}

func validatePropertyValueForStorage(value interface{}) error {
	switch v := value.(type) {
	case nil,
		string,
		bool,
		int,
		int32,
		int64,
		float32,
		float64,
		time.Time:
		return nil
	case []interface{}:
		for i, item := range v {
			if err := validatePropertyValueForStorage(item); err != nil {
				return fmt.Errorf("index %d: %w", i, err)
			}
		}
		return nil
	case []string, []int, []int32, []int64, []float32, []float64, []bool:
		return nil
	case map[string]interface{}:
		for key, item := range v {
			if err := validatePropertyValueForStorage(item); err != nil {
				return fmt.Errorf("key %q: %w", key, err)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported property value type %T", value)
	}
}
