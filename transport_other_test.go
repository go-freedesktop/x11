// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build !unix

package x11

import (
	"errors"
	"testing"
)

func TestDialUnixIsUnavailableHere(t *testing.T) {
	if _, err := DialUnix("/tmp/.X11-unix/X0"); !errors.Is(err, ErrNoTransport) {
		t.Fatalf("DialUnix reported %v, want ErrNoTransport", err)
	}
}

// WrapUnix has nothing to add on a platform with no SCM_RIGHTS, and says so by
// returning nil rather than by pretending to be an fd-passing transport.
func TestWrapUnixAddsNothingHere(t *testing.T) {
	if got := WrapUnix(nil); got != nil {
		t.Fatalf("WrapUnix = %v, want nil", got)
	}
}
