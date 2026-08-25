// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

package x11

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Handshake runs the client connection setup over rw: it sends the byte-order
// sentinel, protocol 11.0 and the authorization name and data, then reads and
// parses the server's reply. order selects the wire byte order; both are valid
// and the server adopts the client's choice for the whole session.
//
// It returns only the parsed [Setup]. Framing requests and demultiplexing
// replies, errors and events is the caller's business, and the two things a
// client does with a connection — pump events, or push frames — want different
// machines over the same socket. What is common is everything up to this
// point, which is what this function is.
//
// On return, rw is positioned exactly at the first byte the server sends after
// the setup reply, so the caller can start reading packets immediately. On
// error rw is left as it is: the caller closes it.
func Handshake(rw io.ReadWriteCloser, order ByteOrder, authName string, authData []byte) (*Setup, error) {
	sentinel := byte(OrderMSB)
	if order == binary.LittleEndian {
		sentinel = OrderLSB
	}
	req := buildSetupRequest(order, sentinel, authName, authData)
	if _, err := rw.Write(req); err != nil {
		return nil, err
	}

	var hdr [8]byte
	if err := ReadFull(rw, hdr[:]); err != nil {
		return nil, err
	}
	status := hdr[0]
	// The additional-data length, in 4-byte units, sits at bytes 6..7 in the
	// client's chosen order. It is read whatever the status is, because even a
	// refusal carries its reason in that block and leaving it unread would
	// desynchronise a caller that retries on the same socket.
	addLen := int(order.Uint16(hdr[6:8])) * 4
	body := make([]byte, addLen)
	if err := ReadFull(rw, body); err != nil {
		return nil, err
	}

	switch status {
	case setupFailed:
		reasonLen := int(hdr[1])
		reason := ""
		if reasonLen <= len(body) {
			reason = string(body[:reasonLen])
		}
		return nil, &SetupError{Reason: reason}
	case setupAuthenticate:
		return nil, &SetupError{Reason: TrimNul(body), Authenticate: true}
	case setupSuccess:
	default:
		return nil, fmt.Errorf("x11: unknown setup status %d", status)
	}

	s, err := parseSetupReply(order, body)
	if err != nil {
		return nil, err
	}
	s.ProtoMajor = order.Uint16(hdr[2:4])
	s.ProtoMinor = order.Uint16(hdr[4:6])
	return s, nil
}

// The three setup-reply statuses (X11 protocol, Connection Setup).
const (
	setupFailed       = 0
	setupSuccess      = 1
	setupAuthenticate = 2
)

// SetupError is the connection-setup refusal: the server would not talk to us
// at all. Reason is the server's own wording, which for a missing or wrong
// cookie is "No protocol specified" or "Authorization required".
type SetupError struct {
	Reason       string
	Authenticate bool // the server asked for further authentication
}

// Error renders the refusal.
func (e *SetupError) Error() string {
	if e.Authenticate {
		return fmt.Sprintf("x11: server requires further authentication: %s", e.Reason)
	}
	return fmt.Sprintf("x11: server refused the connection: %s", e.Reason)
}
