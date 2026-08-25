// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

package x11

import (
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// The handshake is proved in both wire orders, because the sentinel and every
// multi-byte field of the request depend on the client's choice and a server
// that adopted the wrong one would still answer something.
func TestHandshakeSucceedsInBothOrders(t *testing.T) {
	for _, order := range []ByteOrder{binary.LittleEndian, binary.BigEndian} {
		body := defaultSetupBody(order)
		f := newFakeTransport(setupPacket(order, setupSuccess, body, 0))
		s, err := Handshake(f, order, AuthMITCookie, []byte("0123456789abcdef"))
		if err != nil {
			t.Fatalf("%v: Handshake: %v", order, err)
		}
		if s.ProtoMajor != 11 || s.ProtoMinor != 0 {
			t.Errorf("%v: protocol %d.%d, want 11.0", order, s.ProtoMajor, s.ProtoMinor)
		}
		if s.Vendor != "fake" || len(s.Screens) != 1 || s.Screens[0].Width != 1920 {
			t.Errorf("%v: setup = %+v", order, s)
		}
		// The request the client actually put on the wire must carry the
		// sentinel matching the order it asked for.
		sent := f.out.Bytes()
		want := byte(OrderMSB)
		if order == binary.ByteOrder(binary.LittleEndian) {
			want = OrderLSB
		}
		if sent[0] != want {
			t.Errorf("%v: sentinel = %q, want %q", order, sent[0], want)
		}
	}
}

func TestHandshakeRefusal(t *testing.T) {
	order := binary.LittleEndian
	reason := "No protocol specified"
	// The reason lives in the additional-data block, whose length is stated in
	// 4-byte units, so it is padded out.
	body := make([]byte, Pad4(len(reason)))
	copy(body, reason)
	f := newFakeTransport(setupPacket(order, setupFailed, body, byte(len(reason))))

	_, err := Handshake(f, order, "", nil)
	var se *SetupError
	if !errors.As(err, &se) {
		t.Fatalf("Handshake reported %v (%T), want a *SetupError", err, err)
	}
	if se.Reason != reason || se.Authenticate {
		t.Errorf("SetupError = %+v", se)
	}
	if !strings.Contains(se.Error(), "refused the connection") {
		t.Errorf("SetupError.Error() = %q", se.Error())
	}
}

// A refusal whose stated reason length exceeds the block it lives in must not
// be trusted into a slice bound: the reason is dropped, not read past the end.
func TestHandshakeRefusalWithAnOverlongReasonLength(t *testing.T) {
	order := binary.LittleEndian
	f := newFakeTransport(setupPacket(order, setupFailed, make([]byte, 4), 200))
	_, err := Handshake(f, order, "", nil)
	var se *SetupError
	if !errors.As(err, &se) {
		t.Fatalf("Handshake reported %v, want a *SetupError", err)
	}
	if se.Reason != "" {
		t.Errorf("reason = %q, want it dropped", se.Reason)
	}
}

func TestHandshakeAuthenticate(t *testing.T) {
	order := binary.LittleEndian
	body := make([]byte, 12)
	copy(body, "Authorization required")
	f := newFakeTransport(setupPacket(order, setupAuthenticate, body, 0))

	_, err := Handshake(f, order, "", nil)
	var se *SetupError
	if !errors.As(err, &se) {
		t.Fatalf("Handshake reported %v, want a *SetupError", err)
	}
	if !se.Authenticate {
		t.Error("SetupError.Authenticate is false on an Authenticate reply")
	}
	if !strings.Contains(se.Error(), "further authentication") {
		t.Errorf("SetupError.Error() = %q", se.Error())
	}
}

func TestHandshakeUnknownStatus(t *testing.T) {
	order := binary.LittleEndian
	f := newFakeTransport(setupPacket(order, 7, nil, 0))
	if _, err := Handshake(f, order, "", nil); err == nil ||
		!strings.Contains(err.Error(), "unknown setup status") {
		t.Fatalf("Handshake reported %v, want an unknown-status error", err)
	}
}

func TestHandshakeTransportFailures(t *testing.T) {
	order := binary.LittleEndian
	full := setupPacket(order, setupSuccess, defaultSetupBody(order), 0)

	t.Run("write", func(t *testing.T) {
		f := newFakeTransport(full)
		f.writeErr = errInjected
		if _, err := Handshake(f, order, "", nil); !errors.Is(err, errInjected) {
			t.Fatalf("Handshake reported %v, want the injected write error", err)
		}
	})
	t.Run("header", func(t *testing.T) {
		f := newFakeTransport(full[:4]) // half a header
		f.readErr = errInjected
		if _, err := Handshake(f, order, "", nil); err == nil {
			t.Fatal("Handshake accepted a truncated setup header")
		}
	})
	t.Run("body", func(t *testing.T) {
		f := newFakeTransport(full[:8+8]) // header announces more than it sends
		f.readErr = errInjected
		if _, err := Handshake(f, order, "", nil); err == nil {
			t.Fatal("Handshake accepted a truncated setup body")
		}
	})
	t.Run("unparseable body", func(t *testing.T) {
		// A Success whose body is well-framed but describes no screen.
		body := buildSetupBody(order, setupSpec{Vendor: "x"})
		f := newFakeTransport(setupPacket(order, setupSuccess, body, 0))
		if _, err := Handshake(f, order, "", nil); err == nil {
			t.Fatal("Handshake accepted a setup reply with no screens")
		}
	})
}
