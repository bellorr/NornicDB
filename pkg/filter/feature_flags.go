// Package filter feature flags for experimental functionality.
//
// The Kalman filter integration is experimental and must be explicitly enabled.
// This allows A/B testing and comparison of scoring with/without filtering.
//
// Usage:
//
//	// Enable globally
//	filter.EnableKalmanFiltering()
//
//	// Check if enabled
//	if filter.IsKalmanEnabled() {
//		filtered := kalmanFilter.Process(rawScore, target)
//	} else {
//		filtered = rawScore // Pass through unchanged
//	}
//
//	// Disable for testing
//	filter.DisableKalmanFiltering()
//
//	// Use scoped enable (thread-safe for testing)
//	cleanup := filter.WithKalmanEnabled()
//	defer cleanup()
//
// Environment variable:
//
//	NORNICDB_KALMAN_ENABLED=true
package filter

import (
	"os"
	"sync"
	"sync/atomic"
)

// Feature flag keys
const (
	// EnvKalmanEnabled is the environment variable to enable Kalman filtering
	EnvKalmanEnabled = "NORNICDB_KALMAN_ENABLED"

	// FeatureKalmanDecay enables Kalman filtering for memory decay prediction
	FeatureKalmanDecay = "kalman_decay"

	// FeatureKalmanCoAccess enables Kalman filtering for co-access confidence
	FeatureKalmanCoAccess = "kalman_coaccess"

	// FeatureKalmanLatency enables Kalman filtering for latency prediction
	FeatureKalmanLatency = "kalman_latency"

	// FeatureKalmanSimilarity enables Kalman filtering for similarity smoothing
	FeatureKalmanSimilarity = "kalman_similarity"

	// FeatureKalmanTemporal enables Kalman filtering for temporal patterns
	FeatureKalmanTemporal = "kalman_temporal"
)

var (
	// Global feature flag state
	kalmanEnabled     atomic.Bool
	featureFlags      = make(map[string]bool)
	featureFlagsMu    sync.RWMutex
	initOnce          sync.Once
)

func init() {
	// Check environment variable on startup
	initOnce.Do(func() {
		if env := os.Getenv(EnvKalmanEnabled); env == "true" || env == "1" {
			kalmanEnabled.Store(true)
		}
	})
}

// EnableKalmanFiltering globally enables Kalman filtering.
// This is the master switch - individual features can still be disabled.
func EnableKalmanFiltering() {
	kalmanEnabled.Store(true)
}

// DisableKalmanFiltering globally disables Kalman filtering.
func DisableKalmanFiltering() {
	kalmanEnabled.Store(false)
}

// IsKalmanEnabled returns true if Kalman filtering is globally enabled.
func IsKalmanEnabled() bool {
	return kalmanEnabled.Load()
}

// SetKalmanEnabled sets the global Kalman filtering state.
func SetKalmanEnabled(enabled bool) {
	kalmanEnabled.Store(enabled)
}

// WithKalmanEnabled temporarily enables Kalman filtering and returns a cleanup function.
// Useful for tests that need to enable/disable filtering.
//
// Example:
//
//	cleanup := filter.WithKalmanEnabled()
//	defer cleanup()
//	// ... test code with Kalman enabled ...
func WithKalmanEnabled() func() {
	prev := kalmanEnabled.Load()
	kalmanEnabled.Store(true)
	return func() {
		kalmanEnabled.Store(prev)
	}
}

// WithKalmanDisabled temporarily disables Kalman filtering and returns a cleanup function.
func WithKalmanDisabled() func() {
	prev := kalmanEnabled.Load()
	kalmanEnabled.Store(false)
	return func() {
		kalmanEnabled.Store(prev)
	}
}

// EnableFeature enables a specific Kalman feature.
func EnableFeature(feature string) {
	featureFlagsMu.Lock()
	defer featureFlagsMu.Unlock()
	featureFlags[feature] = true
}

// DisableFeature disables a specific Kalman feature.
func DisableFeature(feature string) {
	featureFlagsMu.Lock()
	defer featureFlagsMu.Unlock()
	featureFlags[feature] = false
}

// IsFeatureEnabled returns true if a specific feature is enabled.
// Both the global Kalman flag AND the specific feature must be enabled.
func IsFeatureEnabled(feature string) bool {
	if !kalmanEnabled.Load() {
		return false
	}
	featureFlagsMu.RLock()
	defer featureFlagsMu.RUnlock()
	enabled, exists := featureFlags[feature]
	// If feature not explicitly set, default to enabled when global is on
	if !exists {
		return true
	}
	return enabled
}

// EnableAllFeatures enables all Kalman features.
func EnableAllFeatures() {
	EnableKalmanFiltering()
	EnableFeature(FeatureKalmanDecay)
	EnableFeature(FeatureKalmanCoAccess)
	EnableFeature(FeatureKalmanLatency)
	EnableFeature(FeatureKalmanSimilarity)
	EnableFeature(FeatureKalmanTemporal)
}

// DisableAllFeatures disables all Kalman features.
func DisableAllFeatures() {
	DisableKalmanFiltering()
	DisableFeature(FeatureKalmanDecay)
	DisableFeature(FeatureKalmanCoAccess)
	DisableFeature(FeatureKalmanLatency)
	DisableFeature(FeatureKalmanSimilarity)
	DisableFeature(FeatureKalmanTemporal)
}

// ResetFeatureFlags resets all feature flags to defaults.
func ResetFeatureFlags() {
	kalmanEnabled.Store(false)
	featureFlagsMu.Lock()
	defer featureFlagsMu.Unlock()
	featureFlags = make(map[string]bool)
}

// GetEnabledFeatures returns a list of enabled features.
func GetEnabledFeatures() []string {
	if !kalmanEnabled.Load() {
		return nil
	}
	featureFlagsMu.RLock()
	defer featureFlagsMu.RUnlock()
	
	var enabled []string
	allFeatures := []string{
		FeatureKalmanDecay,
		FeatureKalmanCoAccess,
		FeatureKalmanLatency,
		FeatureKalmanSimilarity,
		FeatureKalmanTemporal,
	}
	
	for _, f := range allFeatures {
		flag, exists := featureFlags[f]
		if !exists || flag {
			enabled = append(enabled, f)
		}
	}
	return enabled
}

// FeatureStatus returns the current status of all features.
type FeatureStatus struct {
	GlobalEnabled bool
	Features      map[string]bool
}

// GetFeatureStatus returns the complete feature status.
func GetFeatureStatus() FeatureStatus {
	featureFlagsMu.RLock()
	defer featureFlagsMu.RUnlock()
	
	status := FeatureStatus{
		GlobalEnabled: kalmanEnabled.Load(),
		Features:      make(map[string]bool),
	}
	
	for k, v := range featureFlags {
		status.Features[k] = v
	}
	return status
}

// FilteredValue represents a value that may or may not have been Kalman filtered.
// Useful for A/B testing and comparison.
type FilteredValue struct {
	Raw       float64 // Original unfiltered value
	Filtered  float64 // Kalman filtered value (same as Raw if disabled)
	WasFiltered bool   // True if Kalman filtering was applied
	Feature   string  // Which feature flag controlled this
}

// Process applies Kalman filtering if enabled, otherwise returns raw value.
// This is the main entry point for feature-flagged filtering.
func (k *Kalman) ProcessIfEnabled(feature string, measurement, target float64) FilteredValue {
	result := FilteredValue{
		Raw:     measurement,
		Feature: feature,
	}
	
	if IsFeatureEnabled(feature) {
		result.Filtered = k.Process(measurement, target)
		result.WasFiltered = true
	} else {
		result.Filtered = measurement
		result.WasFiltered = false
	}
	
	return result
}

// PredictIfEnabled returns prediction if enabled, otherwise returns current state.
func (k *Kalman) PredictIfEnabled(feature string, steps int) FilteredValue {
	result := FilteredValue{
		Raw:     k.State(),
		Feature: feature,
	}
	
	if IsFeatureEnabled(feature) {
		result.Filtered = k.Predict(steps)
		result.WasFiltered = true
	} else {
		result.Filtered = k.State()
		result.WasFiltered = false
	}
	
	return result
}
