//go:build !cuda || !(linux || windows)
// +build !cuda !linux,!windows

// Package cuda provides NVIDIA GPU acceleration using CUDA.
// This is a stub implementation for systems without CUDA support.
package cuda

import (
	"errors"
)

// Errors
var (
	ErrCUDANotAvailable = errors.New("cuda: CUDA is not available (build without cuda tag or unsupported platform)")
	ErrDeviceCreation   = errors.New("cuda: failed to create CUDA device")
	ErrBufferCreation   = errors.New("cuda: failed to create buffer")
	ErrKernelExecution  = errors.New("cuda: kernel execution failed")
	ErrInvalidBuffer    = errors.New("cuda: invalid buffer")
)

// MemoryType defines how buffer memory is managed.
type MemoryType int

const (
	MemoryDevice MemoryType = 0
	MemoryPinned MemoryType = 1
)

// Device represents a CUDA GPU device (stub).
type Device struct{}

// Buffer represents a CUDA memory buffer (stub).
type Buffer struct{}

// SearchResult holds a similarity search result.
type SearchResult struct {
	Index uint32
	Score float32
}

// IsAvailable returns false on systems without CUDA.
func IsAvailable() bool {
	return false
}

// DeviceCount returns 0 on systems without CUDA.
func DeviceCount() int {
	return 0
}

// NewDevice returns an error on systems without CUDA.
func NewDevice(deviceID int) (*Device, error) {
	return nil, ErrCUDANotAvailable
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

// ComputeCapability returns 0, 0.
func (d *Device) ComputeCapability() (int, int) { return 0, 0 }

// NewBuffer returns an error.
func (d *Device) NewBuffer(data []float32, memType MemoryType) (*Buffer, error) {
	return nil, ErrCUDANotAvailable
}

// NewEmptyBuffer returns an error.
func (d *Device) NewEmptyBuffer(count uint64, memType MemoryType) (*Buffer, error) {
	return nil, ErrCUDANotAvailable
}

// Release is a no-op stub.
func (b *Buffer) Release() {}

// Size returns 0.
func (b *Buffer) Size() uint64 { return 0 }

// ReadFloat32 returns nil.
func (b *Buffer) ReadFloat32(count int) []float32 { return nil }

// NormalizeVectors returns an error.
func (d *Device) NormalizeVectors(vectors *Buffer, n, dimensions uint32) error {
	return ErrCUDANotAvailable
}

// CosineSimilarity returns an error.
func (d *Device) CosineSimilarity(embeddings, query, scores *Buffer, n, dimensions uint32, normalized bool) error {
	return ErrCUDANotAvailable
}

// TopK returns an error.
func (d *Device) TopK(scores *Buffer, n, k uint32) ([]uint32, []float32, error) {
	return nil, nil, ErrCUDANotAvailable
}

// Search returns an error.
func (d *Device) Search(embeddings *Buffer, query []float32, n, dimensions uint32, k int, normalized bool) ([]SearchResult, error) {
	return nil, ErrCUDANotAvailable
}
