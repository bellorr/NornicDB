//go:build !vulkan
// +build !vulkan

// Package vulkan provides cross-platform GPU acceleration using Vulkan Compute.
// This is a stub implementation for systems without Vulkan support.
package vulkan

import (
	"errors"
)

// Errors
var (
	ErrVulkanNotAvailable = errors.New("vulkan: Vulkan is not available (build without vulkan tag)")
	ErrDeviceCreation     = errors.New("vulkan: failed to create Vulkan device")
	ErrBufferCreation     = errors.New("vulkan: failed to create buffer")
	ErrKernelExecution    = errors.New("vulkan: kernel execution failed")
	ErrInvalidBuffer      = errors.New("vulkan: invalid buffer")
)

// Device represents a Vulkan GPU device (stub).
type Device struct{}

// Buffer represents a Vulkan memory buffer (stub).
type Buffer struct{}

// SearchResult holds a similarity search result.
type SearchResult struct {
	Index uint32
	Score float32
}

// IsAvailable returns false on systems without Vulkan.
func IsAvailable() bool {
	return false
}

// DeviceCount returns 0 on systems without Vulkan.
func DeviceCount() int {
	return 0
}

// NewDevice returns an error on systems without Vulkan.
func NewDevice(deviceID int) (*Device, error) {
	return nil, ErrVulkanNotAvailable
}

// Release is a no-op stub.
func (d *Device) Release() {}

// ID returns 0.
func (d *Device) ID() int { return 0 }

// Name returns empty string.
func (d *Device) Name() string { return "" }

// MemoryBytes returns 0.
func (d *Device) MemoryBytes() uint64 { return 0 }

// MemoryMB returns 0.
func (d *Device) MemoryMB() int { return 0 }

// NewBuffer returns an error.
func (d *Device) NewBuffer(data []float32) (*Buffer, error) {
	return nil, ErrVulkanNotAvailable
}

// NewEmptyBuffer returns an error.
func (d *Device) NewEmptyBuffer(count uint64) (*Buffer, error) {
	return nil, ErrVulkanNotAvailable
}

// Release is a no-op stub.
func (b *Buffer) Release() {}

// Size returns 0.
func (b *Buffer) Size() uint64 { return 0 }

// ReadFloat32 returns nil.
func (b *Buffer) ReadFloat32(count int) []float32 { return nil }

// NormalizeVectors returns an error.
func (d *Device) NormalizeVectors(vectors *Buffer, n, dimensions uint32) error {
	return ErrVulkanNotAvailable
}

// CosineSimilarity returns an error.
func (d *Device) CosineSimilarity(embeddings, query, scores *Buffer, n, dimensions uint32, normalized bool) error {
	return ErrVulkanNotAvailable
}

// TopK returns an error.
func (d *Device) TopK(scores *Buffer, n, k uint32) ([]uint32, []float32, error) {
	return nil, nil, ErrVulkanNotAvailable
}

// Search returns an error.
func (d *Device) Search(embeddings *Buffer, query []float32, n, dimensions uint32, k int, normalized bool) ([]SearchResult, error) {
	return nil, ErrVulkanNotAvailable
}
