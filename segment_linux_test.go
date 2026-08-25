// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build linux

package x11

import (
	"errors"
	"testing"
)

// On Linux the segment is proved over the REAL thing — an unlinked file on a
// tmpfs, mapped MAP_SHARED — because that is the only mapping an X server can
// be handed. The failure branches still need forcing: mmap and munmap of a
// mapping this process just made do not fail on a healthy kernel.

func TestSegmentIsRealWritableSharedMemory(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	seg, err := NewSegment(0x600, 8192)
	if err != nil {
		t.Fatalf("NewSegment: %v", err)
	}
	if seg.Seg != 0x600 || seg.Size() != 8192 || len(seg.Data) != 8192 || seg.FD < 0 {
		t.Fatalf("segment = %+v (data %d bytes)", seg, len(seg.Data))
	}
	seg.Data[0], seg.Data[8191] = 0x5a, 0xa5 // writable through the mapping
	if err := seg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close is idempotent: a deferred Close after an explicit one is harmless.
	if err := seg.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestNewSegmentPropagatesTheAllocationFailure(t *testing.T) {
	if _, err := NewSegment(1, 0); err == nil {
		t.Error("a zero-size segment should fail in createAnonFile")
	}
}

func TestNewSegmentClosesTheDescriptorWhenMmapFails(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	origMmap, origClose := mmapRegion, closeFD
	t.Cleanup(func() { mmapRegion, closeFD = origMmap, origClose })

	var closed []int
	mmapRegion = func(int, int) ([]byte, error) { return nil, errors.New("mmap refused") }
	closeFD = func(fd int) error { closed = append(closed, fd); return origClose(fd) }
	if _, err := NewSegment(1, 4096); err == nil {
		t.Fatal("NewSegment ignored an mmap failure")
	}
	// The descriptor must not leak: an unlinked file whose last descriptor is
	// never closed keeps its pages until the process exits.
	if len(closed) != 1 {
		t.Errorf("the descriptor was closed %d times, want once", len(closed))
	}
}

func TestSegmentCloseReportsTheFirstError(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	seg, err := NewSegment(1, 4096)
	if err != nil {
		t.Fatalf("NewSegment: %v", err)
	}
	origMunmap, origClose := munmapRegion, closeFD
	t.Cleanup(func() { munmapRegion, closeFD = origMunmap, origClose })

	unmapErr, closeErr := errors.New("munmap boom"), errors.New("close boom")
	data := seg.Data
	fd := seg.FD
	closeCalled := false
	munmapRegion = func([]byte) error { return unmapErr }
	closeFD = func(int) error { closeCalled = true; return closeErr }

	if err := seg.Close(); !errors.Is(err, unmapErr) {
		t.Errorf("Close reported %v, want the munmap error (the first one)", err)
	}
	if !closeCalled {
		t.Error("Close skipped the descriptor after the unmap failed; both steps must be attempted")
	}
	if seg.Data != nil || seg.FD != -1 {
		t.Errorf("Close left data=%v fd=%d; both must be cleared", seg.Data != nil, seg.FD)
	}
	// Release the resources for real, so the test leaks neither a mapping nor
	// a descriptor.
	_ = origMunmap(data)
	_ = origClose(fd)
}

func TestSegmentCloseReportsAnFDErrorAlone(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	seg, err := NewSegment(1, 4096)
	if err != nil {
		t.Fatalf("NewSegment: %v", err)
	}
	origClose := closeFD
	t.Cleanup(func() { closeFD = origClose })

	// Unmap for real, then fail only the descriptor close.
	if err := munmapRegion(seg.Data); err != nil {
		t.Fatalf("munmap: %v", err)
	}
	fd := seg.FD
	seg.Data = nil
	closeErr := errors.New("close boom")
	closeFD = func(int) error { return closeErr }
	if err := seg.Close(); !errors.Is(err, closeErr) {
		t.Errorf("Close reported %v, want the fd-close error", err)
	}
	_ = origClose(fd)
}
