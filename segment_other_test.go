// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build !linux

package x11

import (
	"errors"
	"testing"
)

// Off Linux there is no shared segment to make, so the primitives are package
// variables and a test substitutes a plain heap-backed one. That is not a
// simulation for its own sake: it is what proves the segment LIFECYCLE — who
// unmaps, who closes, in what order, and what a failure of either reports —
// on the platforms where the syscalls do not exist, so a lifecycle bug is
// caught by the darwin and windows lanes too.

// stubSegmentPrimitives installs heap-backed primitives and restores the real
// ones afterwards. It reports how many times each was called.
func stubSegmentPrimitives(t *testing.T) (unmapped, closed *int) {
	t.Helper()
	origAnon, origMmap := createAnonFile, mmapRegion
	origMunmap, origClose := munmapRegion, closeFD
	t.Cleanup(func() {
		createAnonFile, mmapRegion = origAnon, origMmap
		munmapRegion, closeFD = origMunmap, origClose
	})
	var u, c int
	createAnonFile = func(size int) (int, error) { return 7, nil }
	mmapRegion = func(fd, size int) ([]byte, error) { return make([]byte, size), nil }
	munmapRegion = func(b []byte) error { u++; return nil }
	closeFD = func(fd int) error { c++; return nil }
	return &u, &c
}

func TestSegmentIsUnavailableWithoutSubstitution(t *testing.T) {
	if _, err := NewSegment(1, 4096); !errors.Is(err, ErrNoSharedMemory) {
		t.Fatalf("NewSegment reported %v, want ErrNoSharedMemory", err)
	}
}

func TestSegmentLifecycle(t *testing.T) {
	unmapped, closed := stubSegmentPrimitives(t)
	seg, err := NewSegment(0x600, 4096)
	if err != nil {
		t.Fatalf("NewSegment: %v", err)
	}
	if seg.Seg != 0x600 || seg.FD != 7 || seg.Size() != 4096 || len(seg.Data) != 4096 {
		t.Fatalf("segment = %+v (data %d bytes)", seg, len(seg.Data))
	}
	if err := seg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if *unmapped != 1 || *closed != 1 {
		t.Errorf("Close unmapped %d times and closed %d; want once each", *unmapped, *closed)
	}
	// Idempotent: a second Close touches nothing.
	if err := seg.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if *unmapped != 1 || *closed != 1 {
		t.Errorf("a second Close acted again: unmapped %d, closed %d", *unmapped, *closed)
	}
}

func TestSegmentAllocationFailures(t *testing.T) {
	origAnon, origMmap := createAnonFile, mmapRegion
	origClose := closeFD
	t.Cleanup(func() {
		createAnonFile, mmapRegion, closeFD = origAnon, origMmap, origClose
	})

	want := errors.New("no space")
	createAnonFile = func(int) (int, error) { return -1, want }
	if _, err := NewSegment(1, 4096); !errors.Is(err, want) {
		t.Fatalf("NewSegment reported %v, want the allocation failure", err)
	}

	// When the mapping fails the descriptor must still be closed, or an
	// unlinked file's pages stay alive until the process exits.
	var closedFDs []int
	createAnonFile = func(int) (int, error) { return 9, nil }
	mmapRegion = func(int, int) ([]byte, error) { return nil, errors.New("mmap refused") }
	closeFD = func(fd int) error { closedFDs = append(closedFDs, fd); return nil }
	if _, err := NewSegment(1, 4096); err == nil {
		t.Fatal("NewSegment ignored an mmap failure")
	}
	if len(closedFDs) != 1 || closedFDs[0] != 9 {
		t.Errorf("descriptors closed after the mmap failure: %v, want [9]", closedFDs)
	}
}

func TestSegmentCloseReportsTheFirstError(t *testing.T) {
	origAnon, origMmap := createAnonFile, mmapRegion
	origMunmap, origClose := munmapRegion, closeFD
	t.Cleanup(func() {
		createAnonFile, mmapRegion = origAnon, origMmap
		munmapRegion, closeFD = origMunmap, origClose
	})
	createAnonFile = func(int) (int, error) { return 3, nil }
	mmapRegion = func(fd, size int) ([]byte, error) { return make([]byte, size), nil }

	unmapErr, closeErr := errors.New("munmap boom"), errors.New("close boom")
	seg, err := NewSegment(1, 4096)
	if err != nil {
		t.Fatalf("NewSegment: %v", err)
	}
	closeCalled := false
	munmapRegion = func([]byte) error { return unmapErr }
	closeFD = func(int) error { closeCalled = true; return closeErr }
	if err := seg.Close(); !errors.Is(err, unmapErr) {
		t.Errorf("Close reported %v, want the munmap error (the first one)", err)
	}
	if !closeCalled {
		t.Error("Close skipped the descriptor after the unmap failed; both steps must be attempted")
	}

	// And the descriptor's own failure alone, with the unmap succeeding.
	seg2, err := NewSegment(1, 4096)
	if err != nil {
		t.Fatalf("NewSegment: %v", err)
	}
	munmapRegion = func([]byte) error { return nil }
	if err := seg2.Close(); !errors.Is(err, closeErr) {
		t.Errorf("Close reported %v, want the fd-close error", err)
	}
}
