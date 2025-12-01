// Package ui provides the embedded NornicDB web UI assets.
// This file is used for normal builds that include the UI.
//
//go:build !noui

package ui

import "embed"

// Assets contains the built UI files from the dist directory.
// Build the UI with `npm run build` before compiling the Go binary.
//
//go:embed dist/*
var Assets embed.FS
