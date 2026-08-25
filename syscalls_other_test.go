// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build !linux

package x11

import (
	"errors"
	"testing"
)

// The stubs must REPORT their absence, not silently succeed: a consumer that
// takes a nil error here would go on to attach a segment that does not exist.
func TestSharedMemoryIsLinuxOnly(t *testing.T) {
	if _, err := createAnonFile(4096); !errors.Is(err, ErrNoSharedMemory) {
		t.Fatalf("createAnonFile reported %v, want ErrNoSharedMemory", err)
	}
	if _, err := mmapRegion(3, 4096); !errors.Is(err, ErrNoSharedMemory) {
		t.Fatalf("mmapRegion reported %v, want ErrNoSharedMemory", err)
	}
	// Unmapping and closing nothing is not a failure — a caller's cleanup path
	// runs on every platform and must not invent an error there.
	if err := munmapRegion(nil); err != nil {
		t.Fatalf("munmapRegion reported %v", err)
	}
	if err := closeFD(3); err != nil {
		t.Fatalf("closeFD reported %v", err)
	}
}
