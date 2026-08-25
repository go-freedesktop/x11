// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

//go:build !unix

package x11

import (
	"errors"
	"io"
	"net"
)

// ErrNoTransport is reported where there is no unix-domain socket to dial —
// windows, js/wasm, plan9. The wire codec above this line is portable and is
// fully exercised on those platforms against an in-process scripted server;
// what is missing here is only the socket.
var ErrNoTransport = errors.New("x11: dialing an X server over a unix socket is not implemented on this platform")

// WrapUnix reports [ErrNoTransport] by returning nil: there is no SCM_RIGHTS
// on this platform, so there is nothing a wrapper could add to the connection.
// A caller passes its own io.ReadWriteCloser to [Handshake] instead.
func WrapUnix(c *net.UnixConn) io.ReadWriteCloser { return nil }

// DialUnix reports [ErrNoTransport].
func DialUnix(path string) (io.ReadWriteCloser, error) { return nil, ErrNoTransport }
