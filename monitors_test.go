// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

package x11

import (
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// The monitor enumeration is exercised against a scripted in-process server,
// exactly like the setup exchange is: nothing here needs an X server, so a
// mis-encoded RRGetMonitors request or a mis-decoded MONITORINFO is caught on
// darwin and on windows too, and not only on the one lane that can run Xvfb.
//
// Every reply builder below is the inverse of the parser it feeds, so a test
// states what the server said in the terms of the protocol specification
// rather than as a byte blob.

// errNoAnswer is what the scripted server returns for a request the test did
// not script. It is deliberately an error and not a panic: several tests mean
// exactly "this request fails", and they say so by scripting nothing.
var errNoAnswer = errors.New("scripted server: nothing scripted for this request")

// sentRequest is one request the client made, recorded so a test can assert on
// the bytes that went out and not only on what came back.
type sentRequest struct {
	Op     string
	Opcode byte
	Data   byte
	Body   []byte
}

// answer is one scripted server response: a reply packet, or an error.
type answer struct {
	reply []byte
	err   error
}

// scriptedServer is an in-process X server that answers requests by name, in
// the order they were scripted, so a test can give two different answers to
// the two GetAtomName calls of a dual-head enumeration.
type scriptedServer struct {
	order ByteOrder
	queue map[string][]answer
	sent  []sentRequest
}

func newServer(order ByteOrder) *scriptedServer {
	return &scriptedServer{order: order, queue: map[string][]answer{}}
}

// reply scripts one successful answer to the named request.
func (s *scriptedServer) reply(op string, pkt []byte) *scriptedServer {
	s.queue[op] = append(s.queue[op], answer{reply: pkt})
	return s
}

// fail scripts one failing answer to the named request.
func (s *scriptedServer) fail(op string, err error) *scriptedServer {
	s.queue[op] = append(s.queue[op], answer{err: err})
	return s
}

func (s *scriptedServer) Order() ByteOrder { return s.order }

func (s *scriptedServer) Request(op string, opcode, data byte, body []byte) ([]byte, error) {
	s.sent = append(s.sent, sentRequest{Op: op, Opcode: opcode, Data: data, Body: body})
	q := s.queue[op]
	if len(q) == 0 {
		return nil, fmt.Errorf("%w: %s", errNoAnswer, op)
	}
	s.queue[op] = q[1:]
	return q[0].reply, q[0].err
}

// lastBody returns the body of the most recent request named op.
func (s *scriptedServer) lastBody(op string) []byte {
	for i := len(s.sent) - 1; i >= 0; i-- {
		if s.sent[i].Op == op {
			return s.sent[i].Body
		}
	}
	return nil
}

// count reports how many times the named request was sent.
func (s *scriptedServer) count(op string) int {
	n := 0
	for _, r := range s.sent {
		if r.Op == op {
			n++
		}
	}
	return n
}

var _ Requester = (*scriptedServer)(nil)

// newReply frames a reply packet: the 32-byte fixed part (discriminator 1, a
// sequence number, and the additional length in 4-byte units) plus extra.
func newReply(order ByteOrder, extra []byte) []byte {
	pkt := make([]byte, replyHeaderLen+len(extra))
	pkt[0] = 1
	order.PutUint16(pkt[2:4], 1)
	order.PutUint32(pkt[4:8], uint32(len(extra)/4))
	copy(pkt[replyHeaderLen:], extra)
	return pkt
}

// queryExtensionReply is a QueryExtension reply: present flag, major opcode,
// first event and first error.
func queryExtensionReply(order ByteOrder, present bool, major byte) []byte {
	pkt := newReply(order, nil)
	if present {
		pkt[8] = 1
	}
	pkt[9] = major
	pkt[10] = 64  // first-event
	pkt[11] = 128 // first-error
	return pkt
}

// rrVersionReply is an RRQueryVersion reply.
func rrVersionReply(order ByteOrder, major, minor uint32) []byte {
	pkt := newReply(order, nil)
	order.PutUint32(pkt[8:12], major)
	order.PutUint32(pkt[12:16], minor)
	return pkt
}

// internAtomReply is an InternAtom reply.
func internAtomReply(order ByteOrder, atom uint32) []byte {
	pkt := newReply(order, nil)
	order.PutUint32(pkt[8:12], atom)
	return pkt
}

// atomNameReply is a GetAtomName reply: the length in the fixed part, the
// bytes (4-byte padded) in the additional data.
func atomNameReply(order ByteOrder, name string) []byte {
	e := NewEncoder(order)
	e.PutString(name)
	pkt := newReply(order, e.Bytes())
	order.PutUint16(pkt[8:10], uint16(len(name)))
	return pkt
}

// monitorSpec describes one MONITORINFO to encode.
type monitorSpec struct {
	NameAtom          uint32
	Primary           bool
	Automatic         bool
	X, Y              int16
	Width, Height     uint16
	WidthMM, HeightMM uint32
	Outputs           []uint32
}

// encodeMonitors is the exact inverse of decodeMonitors.
func encodeMonitors(order ByteOrder, specs []monitorSpec) []byte {
	e := NewEncoder(order)
	for _, m := range specs {
		e.Put32(m.NameAtom)
		e.Put8(boolByte(m.Primary))
		e.Put8(boolByte(m.Automatic))
		e.Put16(uint16(len(m.Outputs)))
		e.Put16(uint16(m.X))
		e.Put16(uint16(m.Y))
		e.Put16(m.Width)
		e.Put16(m.Height)
		e.Put32(m.WidthMM)
		e.Put32(m.HeightMM)
		for _, o := range m.Outputs {
			e.Put32(o)
		}
	}
	return e.Bytes()
}

func boolByte(b bool) byte {
	if b {
		return 1
	}
	return 0
}

// getMonitorsReply is an RRGetMonitors reply for the given monitors.
func getMonitorsReply(order ByteOrder, specs []monitorSpec) []byte {
	return getMonitorsReplyCount(order, len(specs), encodeMonitors(order, specs))
}

// getMonitorsReplyCount announces count monitors over an arbitrary body, which
// is how a truncated reply is expressed.
func getMonitorsReplyCount(order ByteOrder, count int, body []byte) []byte {
	pkt := newReply(order, body)
	order.PutUint32(pkt[8:12], 12345) // timestamp
	order.PutUint32(pkt[12:16], uint32(count))
	return pkt
}

// outputPropertyReply is an RRGetOutputProperty reply. nItems is stated in
// units of the format, which is what the client has to convert back to bytes.
func outputPropertyReply(order ByteOrder, format byte, value []byte) []byte {
	items := len(value)
	switch format {
	case 16:
		items /= 2
	case 32:
		items /= 4
	}
	return outputPropertyReplyItems(order, format, items, value)
}

// outputPropertyReplyItems announces an arbitrary item count over value, which
// is how an over-announced property is expressed.
func outputPropertyReplyItems(order ByteOrder, format byte, items int, value []byte) []byte {
	e := NewEncoder(order)
	e.PutString(string(value))
	pkt := newReply(order, e.Bytes())
	pkt[1] = format
	order.PutUint32(pkt[8:12], 31) // property type: STRING
	order.PutUint32(pkt[12:16], 0) // bytes-after
	order.PutUint32(pkt[16:20], uint32(items))
	return pkt
}

// xineramaVersionReply is a XineramaQueryVersion reply.
func xineramaVersionReply(order ByteOrder, major, minor uint16) []byte {
	pkt := newReply(order, nil)
	order.PutUint16(pkt[8:10], major)
	order.PutUint16(pkt[10:12], minor)
	return pkt
}

// rect is one XINERAMA ScreenInfo.
type rect struct {
	X, Y          int16
	Width, Height uint16
}

// xineramaScreensReply is a XineramaQueryScreens reply.
func xineramaScreensReply(order ByteOrder, rects []rect) []byte {
	e := NewEncoder(order)
	for _, r := range rects {
		e.Put16(uint16(r.X))
		e.Put16(uint16(r.Y))
		e.Put16(r.Width)
		e.Put16(r.Height)
	}
	return xineramaScreensReplyCount(order, len(rects), e.Bytes())
}

// xineramaScreensReplyCount announces count screens over an arbitrary body.
func xineramaScreensReplyCount(order ByteOrder, count int, body []byte) []byte {
	pkt := newReply(order, body)
	order.PutUint32(pkt[8:12], uint32(count))
	return pkt
}

// edidBlob builds a base EDID block carrying name in descriptor slot idx. A
// name shorter than the 13-byte field is line-feed terminated and
// space-padded, which is what a real display does.
func edidBlob(idx int, name string) []byte {
	b := make([]byte, edidBlockLen)
	copy(b, edidHeader[:])
	d := b[edidDescriptorOff+idx*edidDescriptorLen:][:edidDescriptorLen]
	d[3] = edidTagName
	text := d[5:]
	for i := range text {
		text[i] = ' '
	}
	n := copy(text, name)
	if n < len(text) {
		text[n] = '\n'
	}
	return b
}

// detailedTiming fills descriptor slot idx with a detailed timing, which is
// what a descriptor with a non-zero pixel clock is.
func detailedTiming(b []byte, idx int) {
	d := b[edidDescriptorOff+idx*edidDescriptorLen:][:edidDescriptorLen]
	d[0] = 0x21
	d[1] = 0x39
}

// bothOrders runs fn against both wire orders, because a server speaks the
// order the client asked for and every field above is order-sensitive.
func bothOrders(t *testing.T, fn func(t *testing.T, order ByteOrder)) {
	t.Helper()
	t.Run("little", func(t *testing.T) { fn(t, binary.LittleEndian) })
	t.Run("big", func(t *testing.T) { fn(t, binary.BigEndian) })
}

func TestMonitorDisplayNameAndString(t *testing.T) {
	cases := []struct {
		name string
		m    Monitor
		want string
		str  string
	}{
		{"model wins", Monitor{Name: "DP-2", Model: "DELL U2720Q", Width: 3840, Height: 2160, X: 100},
			"DELL U2720Q", "DELL U2720Q 3840x2160+100+0"},
		{"connector when no model", Monitor{Name: "HDMI-1", Width: 1920, Height: 1080},
			"HDMI-1", "HDMI-1 1920x1080+0+0"},
		{"nameless", Monitor{Width: 800, Height: 600, Y: -600},
			"", "monitor 800x600+0+-600"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.m.DisplayName(); got != c.want {
				t.Errorf("DisplayName() = %q, want %q", got, c.want)
			}
			if got := c.m.String(); got != c.str {
				t.Errorf("String() = %q, want %q", got, c.str)
			}
		})
	}
}

func TestSplitReplyRefusesAShortPacket(t *testing.T) {
	if _, _, err := splitReply("Whatever", make([]byte, replyHeaderLen-1)); err == nil {
		t.Fatal("splitReply accepted a 31-byte reply")
	}
	hdr, extra, err := splitReply("Whatever", make([]byte, replyHeaderLen+8))
	if err != nil {
		t.Fatalf("splitReply: %v", err)
	}
	if len(hdr) != replyHeaderLen || len(extra) != 8 {
		t.Fatalf("split gave %d + %d bytes, want %d + 8", len(hdr), len(extra), replyHeaderLen)
	}
}

func TestQueryExtensionEncodesTheNameAndReadsTheOpcode(t *testing.T) {
	bothOrders(t, func(t *testing.T, order ByteOrder) {
		s := newServer(order).reply("QueryExtension", queryExtensionReply(order, true, 140))
		present, major, err := queryExtension(s, RandrName)
		if err != nil {
			t.Fatalf("queryExtension: %v", err)
		}
		if !present || major != 140 {
			t.Fatalf("present=%v major=%d, want true/140", present, major)
		}
		body := s.lastBody("QueryExtension")
		if order.Uint16(body[0:2]) != uint16(len(RandrName)) {
			t.Errorf("name length %d, want %d", order.Uint16(body[0:2]), len(RandrName))
		}
		// The name is padded to a 4-byte boundary: "RANDR" is 5 bytes, so the
		// body is 4 header bytes plus 8.
		if len(body) != 12 || string(body[4:9]) != RandrName {
			t.Errorf("body %q, want %q padded to 12 bytes", body, RandrName)
		}
	})
}

func TestQueryExtensionFailures(t *testing.T) {
	order := binary.LittleEndian
	if _, _, err := queryExtension(newServer(order), RandrName); err == nil {
		t.Error("a transport failure was not reported")
	}
	short := newServer(order).reply("QueryExtension", make([]byte, 8))
	if _, _, err := queryExtension(short, RandrName); err == nil {
		t.Error("a short reply was not reported")
	}
}

func TestInternAtomAsksOnlyIfExists(t *testing.T) {
	bothOrders(t, func(t *testing.T, order ByteOrder) {
		s := newServer(order).reply("InternAtom", internAtomReply(order, 0x51))
		atom, err := internAtom(s, "EDID")
		if err != nil {
			t.Fatalf("internAtom: %v", err)
		}
		if atom != 0x51 {
			t.Fatalf("atom = %#x, want 0x51", atom)
		}
		if s.sent[0].Data != 1 {
			t.Errorf("only-if-exists byte = %d, want 1: interning EDID must never CREATE it", s.sent[0].Data)
		}
	})
}

func TestInternAtomFailures(t *testing.T) {
	order := binary.LittleEndian
	if _, err := internAtom(newServer(order), "EDID"); err == nil {
		t.Error("a transport failure was not reported")
	}
	short := newServer(order).reply("InternAtom", make([]byte, 4))
	if _, err := internAtom(short, "EDID"); err == nil {
		t.Error("a short reply was not reported")
	}
}

func TestGetAtomName(t *testing.T) {
	bothOrders(t, func(t *testing.T, order ByteOrder) {
		s := newServer(order).reply("GetAtomName", atomNameReply(order, "HDMI-1"))
		name, err := getAtomName(s, 0x40)
		if err != nil {
			t.Fatalf("getAtomName: %v", err)
		}
		if name != "HDMI-1" {
			t.Fatalf("name = %q, want HDMI-1", name)
		}
		if order.Uint32(s.lastBody("GetAtomName")) != 0x40 {
			t.Error("the atom was not encoded into the request")
		}
	})
}

func TestGetAtomNameFailures(t *testing.T) {
	order := binary.LittleEndian
	if _, err := getAtomName(newServer(order), 1); err == nil {
		t.Error("a transport failure was not reported")
	}
	if _, err := getAtomName(newServer(order).reply("GetAtomName", make([]byte, 12)), 1); err == nil {
		t.Error("a short reply was not reported")
	}
	// A reply announcing more name than it carries must not be read past.
	lying := atomNameReply(order, "HDMI-1")
	order.PutUint16(lying[8:10], 99)
	if _, err := getAtomName(newServer(order).reply("GetAtomName", lying), 1); err == nil {
		t.Error("an over-announced name was not reported")
	}
}

// randrServer scripts a server that has RANDR at the given version.
func randrServer(order ByteOrder, major, minor uint32) *scriptedServer {
	return newServer(order).
		reply("QueryExtension", queryExtensionReply(order, true, 140)).
		reply("RRQueryVersion", rrVersionReply(order, major, minor))
}

func TestQueryRandrNegotiatesVersion15(t *testing.T) {
	bothOrders(t, func(t *testing.T, order ByteOrder) {
		s := randrServer(order, 1, 6)
		rr, err := QueryRandr(s)
		if err != nil {
			t.Fatalf("QueryRandr: %v", err)
		}
		if rr == nil || rr.major != 140 || rr.VerMajor != 1 || rr.VerMinor != 6 {
			t.Fatalf("got %+v, want major opcode 140 and version 1.6", rr)
		}
		body := s.lastBody("RRQueryVersion")
		if order.Uint32(body[0:4]) != 1 || order.Uint32(body[4:8]) != 5 {
			t.Errorf("asked for version %d.%d, want 1.5",
				order.Uint32(body[0:4]), order.Uint32(body[4:8]))
		}
	})
}

func TestQueryRandrOnAServerWithout(t *testing.T) {
	order := binary.LittleEndian
	s := newServer(order).reply("QueryExtension", queryExtensionReply(order, false, 0))
	rr, err := QueryRandr(s)
	if err != nil || rr != nil {
		t.Fatalf("QueryRandr = %v, %v; want nil, nil — an absent extension is an answer", rr, err)
	}
}

func TestQueryRandrFailures(t *testing.T) {
	order := binary.LittleEndian
	if _, err := QueryRandr(newServer(order)); err == nil {
		t.Error("a failing QueryExtension was not reported")
	}
	noVersion := newServer(order).reply("QueryExtension", queryExtensionReply(order, true, 140))
	if _, err := QueryRandr(noVersion); err == nil {
		t.Error("a failing RRQueryVersion was not reported")
	}
	short := newServer(order).
		reply("QueryExtension", queryExtensionReply(order, true, 140)).
		reply("RRQueryVersion", make([]byte, 16))
	if _, err := QueryRandr(short); err == nil {
		t.Error("a short RRQueryVersion reply was not reported")
	}
}

func TestHasMonitors(t *testing.T) {
	cases := []struct {
		major, minor uint32
		want         bool
	}{
		{1, 4, false},
		{1, 5, true},
		{1, 6, true},
		{2, 0, true},
		{0, 9, false},
	}
	for _, c := range cases {
		rr := &Randr{VerMajor: c.major, VerMinor: c.minor}
		if got := rr.HasMonitors(); got != c.want {
			t.Errorf("RANDR %d.%d: HasMonitors() = %v, want %v", c.major, c.minor, got, c.want)
		}
	}
}

func TestGetMonitorsDecodesAndNamesThem(t *testing.T) {
	bothOrders(t, func(t *testing.T, order ByteOrder) {
		specs := []monitorSpec{
			{NameAtom: 0x40, Primary: true, Automatic: true, Width: 1920, Height: 1080,
				WidthMM: 509, HeightMM: 286, Outputs: []uint32{0x42}},
			{NameAtom: 0x41, X: 1920, Y: -120, Width: 2560, Height: 1440,
				WidthMM: 597, HeightMM: 336, Outputs: []uint32{0x43, 0x44}},
			{NameAtom: AtomNone, X: -800, Width: 800, Height: 600},
		}
		s := randrServer(order, 1, 5).
			reply("RRGetMonitors", getMonitorsReply(order, specs)).
			reply("GetAtomName", atomNameReply(order, "eDP-1")).
			reply("GetAtomName", atomNameReply(order, "DP-2"))
		rr, err := QueryRandr(s)
		if err != nil {
			t.Fatalf("QueryRandr: %v", err)
		}
		mons, err := rr.GetMonitors(0x100)
		if err != nil {
			t.Fatalf("GetMonitors: %v", err)
		}
		if len(mons) != 3 {
			t.Fatalf("got %d monitors, want 3", len(mons))
		}
		if mons[0].Name != "eDP-1" || !mons[0].Primary || !mons[0].Automatic ||
			mons[0].Width != 1920 || mons[0].HeightMM != 286 ||
			len(mons[0].Outputs) != 1 || mons[0].Outputs[0] != 0x42 {
			t.Errorf("monitor 0 = %+v", mons[0])
		}
		if mons[1].Name != "DP-2" || mons[1].X != 1920 || mons[1].Y != -120 ||
			len(mons[1].Outputs) != 2 {
			t.Errorf("monitor 1 = %+v", mons[1])
		}
		// A monitor with no name atom is not asked about, and stays nameless.
		if mons[2].Name != "" || mons[2].X != -800 || len(mons[2].Outputs) != 0 {
			t.Errorf("monitor 2 = %+v", mons[2])
		}
		if s.count("GetAtomName") != 2 {
			t.Errorf("%d GetAtomName round trips, want 2", s.count("GetAtomName"))
		}
		body := s.lastBody("RRGetMonitors")
		if order.Uint32(body[0:4]) != 0x100 || body[4] != 1 {
			t.Errorf("request body %v, want root 0x100 and get-active=1", body[:8])
		}
	})
}

func TestGetMonitorsKeepsARectangleWhoseNameCannotBeResolved(t *testing.T) {
	order := binary.LittleEndian
	specs := []monitorSpec{{NameAtom: 0x40, Width: 1920, Height: 1080}}
	s := randrServer(order, 1, 5).
		reply("RRGetMonitors", getMonitorsReply(order, specs)).
		fail("GetAtomName", errNoAnswer)
	rr, _ := QueryRandr(s)
	mons, err := rr.GetMonitors(0x100)
	if err != nil {
		t.Fatalf("GetMonitors: %v", err)
	}
	if len(mons) != 1 || mons[0].Name != "" || mons[0].Width != 1920 {
		t.Fatalf("got %+v, want one nameless 1920-wide monitor", mons)
	}
}

func TestGetMonitorsFailures(t *testing.T) {
	order := binary.LittleEndian
	rr, _ := QueryRandr(randrServer(order, 1, 5))
	if _, err := rr.GetMonitors(0x100); err == nil {
		t.Error("a failing RRGetMonitors was not reported")
	}
	shortSrv := randrServer(order, 1, 5).reply("RRGetMonitors", make([]byte, 20))
	rr, _ = QueryRandr(shortSrv)
	if _, err := rr.GetMonitors(0x100); err == nil {
		t.Error("a short RRGetMonitors reply was not reported")
	}
	// Two monitors announced, one encoded.
	body := encodeMonitors(order, []monitorSpec{{NameAtom: AtomNone, Width: 640, Height: 480}})
	truncSrv := randrServer(order, 1, 5).
		reply("RRGetMonitors", getMonitorsReplyCount(order, 2, body))
	rr, _ = QueryRandr(truncSrv)
	if _, err := rr.GetMonitors(0x100); err == nil {
		t.Error("a truncated monitor list was not reported")
	}
}

func TestOutputPropertyFormats(t *testing.T) {
	bothOrders(t, func(t *testing.T, order ByteOrder) {
		cases := []struct {
			format byte
			value  []byte
			want   int
		}{
			{8, []byte("abcde"), 5},
			{16, []byte{1, 2, 3, 4}, 4},
			{32, []byte{1, 2, 3, 4, 5, 6, 7, 8}, 8},
			{0, nil, 0},
		}
		for _, c := range cases {
			t.Run(fmt.Sprintf("format%d", c.format), func(t *testing.T) {
				s := randrServer(order, 1, 5).
					reply("RRGetOutputProperty", outputPropertyReply(order, c.format, c.value))
				rr, _ := QueryRandr(s)
				format, value, err := rr.OutputProperty(0x42, 0x51, 8)
				if err != nil {
					t.Fatalf("OutputProperty: %v", err)
				}
				if format != c.format || len(value) != c.want {
					t.Fatalf("format %d, %d bytes; want %d, %d", format, len(value), c.format, c.want)
				}
				body := s.lastBody("RRGetOutputProperty")
				if len(body) != 24 {
					t.Fatalf("request body is %d bytes, want 24", len(body))
				}
				if order.Uint32(body[0:4]) != 0x42 || order.Uint32(body[4:8]) != 0x51 ||
					order.Uint32(body[16:20]) != 8 || body[20] != 0 || body[21] != 0 {
					t.Errorf("request body %v: output, property, length, delete or pending is wrong", body)
				}
			})
		}
	})
}

func TestOutputPropertyFailures(t *testing.T) {
	order := binary.LittleEndian
	rr, _ := QueryRandr(randrServer(order, 1, 5))
	if _, _, err := rr.OutputProperty(1, 2, 4); err == nil {
		t.Error("a failing RRGetOutputProperty was not reported")
	}
	rr, _ = QueryRandr(randrServer(order, 1, 5).reply("RRGetOutputProperty", make([]byte, 24)))
	if _, _, err := rr.OutputProperty(1, 2, 4); err == nil {
		t.Error("a short reply was not reported")
	}
	rr, _ = QueryRandr(randrServer(order, 1, 5).
		reply("RRGetOutputProperty", outputPropertyReplyItems(order, 8, 999, []byte("abc"))))
	if _, _, err := rr.OutputProperty(1, 2, 4); err == nil {
		t.Error("an over-announced property was not reported")
	}
}

func TestEDIDModelName(t *testing.T) {
	withTiming := edidBlob(1, "DELL U2720Q")
	detailedTiming(withTiming, 0)

	// A name descriptor holding nothing but padding is not a name: the parser
	// must move on to the next descriptor rather than return "".
	empty := edidBlob(0, "")
	copy(empty[edidDescriptorOff+edidDescriptorLen:], edidBlob(1, "VITURE Beast")[edidDescriptorOff+edidDescriptorLen:][:edidDescriptorLen])

	bad := edidBlob(0, "nope")
	bad[0] = 0x01

	// Not every display terminates a short name with the line feed the spec
	// asks for; some just pad the field out with spaces. The padding has to go
	// either way, or the name never compares equal to what the user was told.
	unterminated := edidBlob(0, "Beast")
	unterminated[edidDescriptorOff+5+5] = ' '

	cases := []struct {
		name string
		blob []byte
		want string
	}{
		{"exactly 13 characters, no terminator", edidBlob(0, "ABCDEFGHIJKLM"), "ABCDEFGHIJKLM"},
		{"short name, LF terminated", edidBlob(0, "VITURE Beast"), "VITURE Beast"},
		{"short name, space padded only", unterminated, "Beast"},
		{"after a detailed timing", withTiming, "DELL U2720Q"},
		{"empty descriptor skipped", empty, "VITURE Beast"},
		{"no name descriptor", make([]byte, edidBlockLen), ""},
		{"not an EDID", bad, ""},
		{"too short", []byte{0, 1, 2}, ""},
		{"no descriptors at all", func() []byte { b := edidBlob(0, "X"); b[edidDescriptorOff+3] = 0xff; return b }(), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EDIDModelName(c.blob); got != c.want {
				t.Errorf("EDIDModelName() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestEDIDModelNameKeepsAHeaderlessBlobOut(t *testing.T) {
	// The all-zero blob has neither the signature nor a name, and must not be
	// mistaken for a display with an empty name.
	if got := EDIDModelName(make([]byte, edidBlockLen)); got != "" {
		t.Errorf("an all-zero blob yielded %q", got)
	}
}

func TestResolveModelsReadsTheSecondOutputWhenTheFirstIsSilent(t *testing.T) {
	order := binary.LittleEndian
	s := randrServer(order, 1, 5).
		reply("InternAtom", internAtomReply(order, 0x51)).
		// First output: the property is not there (format 0).
		reply("RRGetOutputProperty", outputPropertyReply(order, 0, nil)).
		// Second output: a real EDID.
		reply("RRGetOutputProperty", outputPropertyReply(order, 8, edidBlob(0, "VITURE Beast")))
	rr, _ := QueryRandr(s)
	mons := []Monitor{{Name: "DP-2", Outputs: []uint32{0x42, 0x43}}}
	if err := rr.ResolveModels(mons); err != nil {
		t.Fatalf("ResolveModels: %v", err)
	}
	if mons[0].Model != "VITURE Beast" {
		t.Fatalf("Model = %q, want VITURE Beast", mons[0].Model)
	}
	if mons[0].DisplayName() != "VITURE Beast" {
		t.Errorf("DisplayName() = %q, want the model", mons[0].DisplayName())
	}
}

func TestResolveModelsIsBestEffort(t *testing.T) {
	order := binary.LittleEndian
	t.Run("no EDID atom on the server", func(t *testing.T) {
		s := randrServer(order, 1, 5).reply("InternAtom", internAtomReply(order, AtomNone))
		rr, _ := QueryRandr(s)
		mons := []Monitor{{Name: "HDMI-1", Outputs: []uint32{0x42}}}
		if err := rr.ResolveModels(mons); err != nil {
			t.Fatalf("ResolveModels: %v", err)
		}
		if mons[0].Model != "" {
			t.Errorf("Model = %q, want empty", mons[0].Model)
		}
		if s.count("RRGetOutputProperty") != 0 {
			t.Error("an unknown atom must not be asked about, output by output")
		}
	})
	t.Run("the property request fails", func(t *testing.T) {
		s := randrServer(order, 1, 5).
			reply("InternAtom", internAtomReply(order, 0x51)).
			fail("RRGetOutputProperty", errNoAnswer)
		rr, _ := QueryRandr(s)
		mons := []Monitor{{Name: "HDMI-1", Outputs: []uint32{0x42}}}
		if err := rr.ResolveModels(mons); err != nil {
			t.Fatalf("ResolveModels: %v", err)
		}
		if mons[0].Model != "" || mons[0].DisplayName() != "HDMI-1" {
			t.Errorf("got %+v, want the connector name to survive", mons[0])
		}
	})
	t.Run("the blob is not an EDID", func(t *testing.T) {
		s := randrServer(order, 1, 5).
			reply("InternAtom", internAtomReply(order, 0x51)).
			reply("RRGetOutputProperty", outputPropertyReply(order, 8, []byte("garbage!")))
		rr, _ := QueryRandr(s)
		mons := []Monitor{{Name: "HDMI-1", Outputs: []uint32{0x42}}}
		if err := rr.ResolveModels(mons); err != nil {
			t.Fatalf("ResolveModels: %v", err)
		}
		if mons[0].Model != "" {
			t.Errorf("Model = %q, want empty", mons[0].Model)
		}
	})
	t.Run("the atom cannot be looked up", func(t *testing.T) {
		s := randrServer(order, 1, 5).fail("InternAtom", errNoAnswer)
		rr, _ := QueryRandr(s)
		if err := rr.ResolveModels([]Monitor{{Outputs: []uint32{1}}}); err == nil {
			t.Error("a failing InternAtom was not reported: the connection is in trouble")
		}
	})
}

func TestQueryXinerama(t *testing.T) {
	bothOrders(t, func(t *testing.T, order ByteOrder) {
		s := newServer(order).
			reply("QueryExtension", queryExtensionReply(order, true, 141)).
			reply("XineramaQueryVersion", xineramaVersionReply(order, 1, 1))
		xin, err := QueryXinerama(s)
		if err != nil {
			t.Fatalf("QueryXinerama: %v", err)
		}
		if xin == nil || xin.major != 141 || xin.VerMajor != 1 || xin.VerMinor != 1 {
			t.Fatalf("got %+v, want major opcode 141 and version 1.1", xin)
		}
	})
}

func TestQueryXineramaFailures(t *testing.T) {
	order := binary.LittleEndian
	absent := newServer(order).reply("QueryExtension", queryExtensionReply(order, false, 0))
	if xin, err := QueryXinerama(absent); xin != nil || err != nil {
		t.Errorf("QueryXinerama = %v, %v; want nil, nil", xin, err)
	}
	if _, err := QueryXinerama(newServer(order)); err == nil {
		t.Error("a failing QueryExtension was not reported")
	}
	noVersion := newServer(order).reply("QueryExtension", queryExtensionReply(order, true, 141))
	if _, err := QueryXinerama(noVersion); err == nil {
		t.Error("a failing XineramaQueryVersion was not reported")
	}
	short := newServer(order).
		reply("QueryExtension", queryExtensionReply(order, true, 141)).
		reply("XineramaQueryVersion", make([]byte, 12))
	if _, err := QueryXinerama(short); err == nil {
		t.Error("a short XineramaQueryVersion reply was not reported")
	}
}

// xineramaServer scripts a server whose only multi-head answer is XINERAMA.
func xineramaServer(order ByteOrder) *scriptedServer {
	return newServer(order).
		reply("QueryExtension", queryExtensionReply(order, true, 141)).
		reply("XineramaQueryVersion", xineramaVersionReply(order, 1, 1))
}

func TestQueryScreens(t *testing.T) {
	bothOrders(t, func(t *testing.T, order ByteOrder) {
		rects := []rect{{Width: 1920, Height: 1080}, {X: 1920, Y: 24, Width: 1280, Height: 1024}}
		s := xineramaServer(order).reply("XineramaQueryScreens", xineramaScreensReply(order, rects))
		xin, _ := QueryXinerama(s)
		mons, err := xin.QueryScreens()
		if err != nil {
			t.Fatalf("QueryScreens: %v", err)
		}
		if len(mons) != 2 {
			t.Fatalf("got %d screens, want 2", len(mons))
		}
		if !mons[0].Primary || mons[0].Width != 1920 {
			t.Errorf("screen 0 = %+v, want the primary 1920-wide one", mons[0])
		}
		if mons[1].Primary || mons[1].X != 1920 || mons[1].Y != 24 || mons[1].Height != 1024 {
			t.Errorf("screen 1 = %+v", mons[1])
		}
		// XINERAMA states no names at all.
		if mons[0].Name != "" || mons[1].Name != "" {
			t.Error("XINERAMA screens must come back nameless")
		}
	})
}

func TestQueryScreensFailures(t *testing.T) {
	order := binary.LittleEndian
	xin, _ := QueryXinerama(xineramaServer(order))
	if _, err := xin.QueryScreens(); err == nil {
		t.Error("a failing XineramaQueryScreens was not reported")
	}
	xin, _ = QueryXinerama(xineramaServer(order).reply("XineramaQueryScreens", make([]byte, 8)))
	if _, err := xin.QueryScreens(); err == nil {
		t.Error("a short reply was not reported")
	}
	xin, _ = QueryXinerama(xineramaServer(order).
		reply("XineramaQueryScreens", xineramaScreensReplyCount(order, 3, make([]byte, 8))))
	if _, err := xin.QueryScreens(); err == nil {
		t.Error("a truncated screen list was not reported")
	}
}

// screen is the setup screen the fallback describes.
var screen = &Screen{Root: 0x100, Width: 1024, Height: 768, WidthMM: 270, HeightMM: 203}

func TestMonitorsPrefersRandrAndFillsInTheModel(t *testing.T) {
	bothOrders(t, func(t *testing.T, order ByteOrder) {
		specs := []monitorSpec{{NameAtom: 0x40, Primary: true, Width: 3840, Height: 1080,
			Outputs: []uint32{0x42}}}
		s := randrServer(order, 1, 5).
			reply("RRGetMonitors", getMonitorsReply(order, specs)).
			reply("GetAtomName", atomNameReply(order, "DP-1")).
			reply("InternAtom", internAtomReply(order, 0x51)).
			reply("RRGetOutputProperty", outputPropertyReply(order, 8, edidBlob(0, "VITURE Beast")))
		mons, err := Monitors(s, screen)
		if err != nil {
			t.Fatalf("Monitors: %v", err)
		}
		if len(mons) != 1 {
			t.Fatalf("got %d monitors, want 1", len(mons))
		}
		if mons[0].Name != "DP-1" || mons[0].Model != "VITURE Beast" || mons[0].Width != 3840 {
			t.Fatalf("got %+v, want DP-1 / VITURE Beast / 3840 wide", mons[0])
		}
		if s.count("XineramaQueryScreens") != 0 {
			t.Error("XINERAMA was consulted even though RANDR answered")
		}
	})
}

func TestMonitorsKeepsTheRectanglesWhenTheModelsCannotBeRead(t *testing.T) {
	order := binary.LittleEndian
	specs := []monitorSpec{{NameAtom: AtomNone, Primary: true, Width: 3840, Height: 1080}}
	s := randrServer(order, 1, 5).
		reply("RRGetMonitors", getMonitorsReply(order, specs)).
		fail("InternAtom", errNoAnswer)
	mons, err := Monitors(s, screen)
	if err != nil {
		t.Fatalf("Monitors: %v", err)
	}
	if len(mons) != 1 || mons[0].Width != 3840 {
		t.Fatalf("got %+v, want the rectangle to survive a failed model lookup", mons)
	}
}

func TestMonitorsFallsBackToXinerama(t *testing.T) {
	order := binary.LittleEndian
	cases := []struct {
		name  string
		build func() *scriptedServer
	}{
		{"no RANDR at all", func() *scriptedServer {
			return newServer(order).
				reply("QueryExtension", queryExtensionReply(order, false, 0)).
				reply("QueryExtension", queryExtensionReply(order, true, 141)).
				reply("XineramaQueryVersion", xineramaVersionReply(order, 1, 1))
		}},
		{"RANDR too old for RRGetMonitors", func() *scriptedServer {
			return randrServer(order, 1, 4).
				reply("QueryExtension", queryExtensionReply(order, true, 141)).
				reply("XineramaQueryVersion", xineramaVersionReply(order, 1, 1))
		}},
		{"RRGetMonitors refused", func() *scriptedServer {
			return randrServer(order, 1, 5).
				fail("RRGetMonitors", errNoAnswer).
				reply("QueryExtension", queryExtensionReply(order, true, 141)).
				reply("XineramaQueryVersion", xineramaVersionReply(order, 1, 1))
		}},
		{"RRGetMonitors answers nothing", func() *scriptedServer {
			return randrServer(order, 1, 5).
				reply("RRGetMonitors", getMonitorsReply(order, nil)).
				reply("QueryExtension", queryExtensionReply(order, true, 141)).
				reply("XineramaQueryVersion", xineramaVersionReply(order, 1, 1))
		}},
		{"QueryExtension itself fails", func() *scriptedServer {
			// Both gates fail; the whole-screen fallback is what is left, and
			// that case is covered separately. Here XINERAMA still answers.
			return newServer(order).
				fail("QueryExtension", errNoAnswer).
				reply("QueryExtension", queryExtensionReply(order, true, 141)).
				reply("XineramaQueryVersion", xineramaVersionReply(order, 1, 1))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.build()
			s.reply("XineramaQueryScreens", xineramaScreensReply(order,
				[]rect{{Width: 1600, Height: 900}, {X: 1600, Width: 1600, Height: 900}}))
			mons, err := Monitors(s, screen)
			if err != nil {
				t.Fatalf("Monitors: %v", err)
			}
			if len(mons) != 2 || mons[0].Width != 1600 || mons[1].X != 1600 {
				t.Fatalf("got %+v, want the two XINERAMA rectangles", mons)
			}
		})
	}
}

func TestMonitorsFallsBackToTheWholeScreen(t *testing.T) {
	order := binary.LittleEndian
	cases := []struct {
		name  string
		build func() *scriptedServer
	}{
		{"a server with neither extension", func() *scriptedServer {
			return newServer(order).
				reply("QueryExtension", queryExtensionReply(order, false, 0)).
				reply("QueryExtension", queryExtensionReply(order, false, 0))
		}},
		{"XINERAMA refuses QueryScreens", func() *scriptedServer {
			return newServer(order).
				reply("QueryExtension", queryExtensionReply(order, false, 0)).
				reply("QueryExtension", queryExtensionReply(order, true, 141)).
				reply("XineramaQueryVersion", xineramaVersionReply(order, 1, 1)).
				fail("XineramaQueryScreens", errNoAnswer)
		}},
		{"XINERAMA reports no screens", func() *scriptedServer {
			return newServer(order).
				reply("QueryExtension", queryExtensionReply(order, false, 0)).
				reply("QueryExtension", queryExtensionReply(order, true, 141)).
				reply("XineramaQueryVersion", xineramaVersionReply(order, 1, 1)).
				reply("XineramaQueryScreens", xineramaScreensReply(order, nil))
		}},
		{"both gates fail", func() *scriptedServer { return newServer(order) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mons, err := Monitors(c.build(), screen)
			if err != nil {
				t.Fatalf("Monitors: %v", err)
			}
			if len(mons) != 1 {
				t.Fatalf("got %d monitors, want the screen itself", len(mons))
			}
			want := Monitor{Primary: true, Width: 1024, Height: 768, WidthMM: 270, HeightMM: 203}
			if !reflect.DeepEqual(mons[0], want) {
				t.Fatalf("got %+v, want %+v", mons[0], want)
			}
		})
	}
}

func TestMonitorsRefusesANilScreen(t *testing.T) {
	_, err := Monitors(newServer(binary.LittleEndian), nil)
	if err == nil {
		t.Fatal("Monitors accepted a nil screen")
	}
	if !strings.Contains(err.Error(), "nil screen") {
		t.Errorf("error %q does not say which screen is missing", err)
	}
}
