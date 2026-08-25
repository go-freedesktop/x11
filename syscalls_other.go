// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build !linux

package x11

import "errors"

// ErrNoSharedMemory is reported off Linux, where no X server is dialed and so
// no shared segment is ever needed. Everything above it — the wire codec, the
// Xauthority parser, the setup exchange — is portable and is fully exercised
// here; only the shared memory and the socket are not.
var ErrNoSharedMemory = errors.New("x11: shared memory segments are only implemented on Linux")

// The shared-memory primitives are package variables off Linux too, so a test
// can substitute a plain heap-backed "segment" and exercise the segment
// lifecycle — allocate, map, unmap, close, and each of their failures — on
// every platform rather than only on the one that has mmap of an unlinked file.
var (
	createAnonFile = func(size int) (int, error) { return -1, ErrNoSharedMemory }
	mmapRegion     = func(fd, size int) ([]byte, error) { return nil, ErrNoSharedMemory }
	munmapRegion   = func(b []byte) error { return nil }
	closeFD        = func(fd int) error { return nil }
)
