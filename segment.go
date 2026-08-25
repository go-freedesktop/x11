// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

package x11

import "fmt"

// Segment is a mapped anonymous shared-memory region backing a MIT-SHM
// attachment: Data is the pixel store both peers see, FD is the descriptor
// handed to the X server by the extension's AttachFd request, and Seg is the
// resource id the server knows it by.
//
// It is the memory, not the protocol. Which AttachFd/PutImage/GetImage
// requests a client sends over it is the client's business — a capture asks
// the server to WRITE into the segment, a window writes into it and asks the
// server to read — and those requests live in the consumer, not here.
//
// The lifecycle is portable; the shared-memory syscalls themselves sit behind
// createAnonFile/mmapRegion/munmapRegion/closeFD, which are provided per
// platform. Off Linux there is no X server to attach to, so createAnonFile
// reports [ErrNoSharedMemory] and no segment is ever created.
type Segment struct {
	Seg  uint32
	FD   int
	Data []byte
	size int
}

// NewSegment allocates and maps a shared-memory segment of size bytes and
// assigns it the resource id seg. The caller registers it with the server via
// the MIT-SHM AttachFd request and frees it with [Segment.Close].
func NewSegment(seg uint32, size int) (*Segment, error) {
	fd, err := createAnonFile(size)
	if err != nil {
		return nil, err
	}
	data, err := mmapRegion(fd, size)
	if err != nil {
		_ = closeFD(fd)
		return nil, fmt.Errorf("x11: shm mmap: %w", err)
	}
	return &Segment{Seg: seg, FD: fd, Data: data, size: size}, nil
}

// Size returns the segment's byte length.
func (s *Segment) Size() int { return s.size }

// Close unmaps the region and closes its descriptor, returning the first error
// (both steps are attempted regardless). It is idempotent, so a deferred Close
// after an explicit one is harmless.
func (s *Segment) Close() error {
	var first error
	if s.Data != nil {
		if err := munmapRegion(s.Data); err != nil && first == nil {
			first = err
		}
		s.Data = nil
	}
	if s.FD >= 0 {
		if err := closeFD(s.FD); err != nil && first == nil {
			first = err
		}
		s.FD = -1
	}
	return first
}
