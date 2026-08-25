// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

package x11

import "time"

// FDSender is implemented by a transport that can pass a file descriptor
// alongside a request over the same socket (a unix-domain stream, via
// SCM_RIGHTS). The transport [WrapUnix] returns implements it; an in-process
// net.Pipe used by a test does not, so a client's MIT-SHM fd-passing path
// degrades to the plain socket path when it is absent.
//
// It is an interface rather than a concrete type because that is what keeps
// the connection above it transport-agnostic: a client type-asserts on this
// and never on a socket.
type FDSender interface {
	// SendFD writes one already-framed request with fd attached as a single
	// SCM_RIGHTS control message.
	SendFD(msg []byte, fd int) error
}

// Waiter is implemented by a transport that can say whether the server sent
// anything, without reading a whole packet. The transport [WrapUnix] returns
// implements it.
//
// It exists because parts of the X11 protocol have no timeout of their own —
// a selection paste asks whoever owns the clipboard and waits for an event
// that arrives only if that owner is alive and still answering. A client that
// cannot bound that wait freezes.
type Waiter interface {
	// WaitReadable reports whether the server sent anything within d. An
	// implementation must not consume a partial packet: see the note on the
	// unix transport for why a read deadline is not a substitute.
	WaitReadable(d time.Duration) bool
}
