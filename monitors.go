// Copyright (c) the go-freedesktop/x11 authors.
// SPDX-License-Identifier: BSD-3-Clause

package x11

import "fmt"

// This file enumerates the MONITORS of an X screen.
//
// An X screen is one coordinate space; the physical monitors are laid out
// inside it. A client that wants "the left-hand display" — to capture it, or
// to put a window full-screen on it — needs the rectangle, and there are two
// ways to ask:
//
//   - RANDR 1.5 RRGetMonitors, which is the modern answer and the only one
//     that carries a NAME ("HDMI-1") and a primary flag.
//   - XINERAMA QueryScreens, which is older, nameless, and still what a few
//     servers (and Xvfb with +xinerama) answer.
//
// A server offering neither has exactly one monitor: the screen itself.
//
// It is the one place in this package with a request table, and it is here
// rather than in each client because the answer does not depend on what the
// client is FOR: a capture and a toolkit want the same rectangles, and two
// copies of a protocol parser drift silently until something fails on one
// back-end only. What it needs of a client is expressed as [Requester] — two
// methods, which any request/reply machine already has — so neither client has
// to give up its own connection type to share this.

// Requester is what the monitor enumeration needs of a connection, and all it
// needs: the negotiated byte order, and the ability to send a request and be
// handed its reply.
//
// This package deliberately owns no connection type (see the package comment),
// because a capture loop and an event pump want genuinely different
// request/reply machines over the same socket. Both of them can satisfy this,
// though, and that is what lets one enumeration serve both.
type Requester interface {
	// Order returns the byte order negotiated at connection setup.
	Order() ByteOrder
	// Request sends one request and returns its reply. op names the request
	// for error messages; opcode is the major opcode — a core one, or an
	// extension's as QueryExtension reported it; data is the byte the protocol
	// packs into the request header. body must already be 4-byte padded.
	//
	// The reply is the 32-byte fixed part followed by whatever additional data
	// the request carries, as one slice. An error reply comes back as an
	// error, not as a packet.
	Request(op string, opcode, data byte, body []byte) ([]byte, error)
}

// Extension names, as QueryExtension spells them.
const (
	RandrName    = "RANDR"
	XineramaName = "XINERAMA"
)

// Core request opcodes this file sends (X11 protocol, appendix B). It sends
// them itself rather than calling back into the client, so that a client with
// its own spelling of InternAtom does not have to expose it here.
const (
	opInternAtom     = 16
	opGetAtomName    = 17
	opQueryExtension = 98
)

// RANDR minor opcodes (randr.h).
const (
	rrReqQueryVersion      = 0
	rrReqGetOutputProperty = 15
	rrReqGetMonitors       = 42
)

// XINERAMA minor opcodes.
const (
	xinReqQueryVersion = 0
	xinReqQueryScreens = 5
)

// replyHeaderLen is the fixed part every reply starts with.
const replyHeaderLen = 32

// AtomNone is the atom that names nothing (X11/Xatom.h).
const AtomNone = 0

// Monitor is one physical output's rectangle inside an X screen.
type Monitor struct {
	// Name is the RANDR monitor name, which is normally the CONNECTOR —
	// "HDMI-1", "DP-2", "eDP-1". It is stable and it is what xrandr prints,
	// but it does not say what is plugged in. It is "" from XINERAMA, which
	// carries no names at all.
	Name string
	// Model is the display's own product name, read out of its EDID: "VITURE
	// Beast", "DELL U2720Q". It is what a user recognises, and the only field
	// that can tell two identical connectors apart by what is on the end of
	// them — so an application that identifies a headset by name wants this
	// one. It is "" when the output publishes no EDID (a virtual server such
	// as Xvfb, or a driver that does not export the property).
	Model string
	// NameAtom is the atom Name was resolved from, kept because a caller that
	// re-queries can compare atoms without a round trip.
	NameAtom uint32
	// Primary marks the monitor the desktop treats as its origin.
	Primary bool
	// Automatic marks a monitor RANDR synthesised from the outputs rather than
	// one configured by hand with `xrandr --setmonitor`.
	Automatic bool
	// X, Y, Width, Height are the monitor's rectangle inside the screen's
	// coordinate space, top-left origin, Y growing downwards.
	X, Y          int16
	Width, Height uint16
	// WidthMM, HeightMM are the physical size, which is what a DPI is computed
	// from. Both are 0 on a display that reports none.
	WidthMM  uint32
	HeightMM uint32
	// Outputs are the RANDR output ids driving this monitor. Empty from
	// XINERAMA and from the whole-screen fallback.
	Outputs []uint32
}

// DisplayName is the best human-readable name for the monitor: the EDID model
// when the display published one, the connector otherwise. It is what to show
// a user choosing an output.
func (m Monitor) DisplayName() string {
	if m.Model != "" {
		return m.Model
	}
	return m.Name
}

// String renders the monitor for logs.
func (m Monitor) String() string {
	name := m.DisplayName()
	if name == "" {
		name = "monitor"
	}
	return fmt.Sprintf("%s %dx%d+%d+%d", name, m.Width, m.Height, m.X, m.Y)
}

// splitReply divides a reply into its 32-byte fixed part and its additional
// data. A reply shorter than the fixed part cannot come off a conforming
// server, but a Requester is an interface: refusing it here is what keeps
// every field read below in bounds without a check of its own.
func splitReply(op string, reply []byte) (hdr, extra []byte, err error) {
	if len(reply) < replyHeaderLen {
		return nil, nil, fmt.Errorf("x11: %s: reply is %d bytes, want at least %d",
			op, len(reply), replyHeaderLen)
	}
	return reply[:replyHeaderLen], reply[replyHeaderLen:], nil
}

// queryExtension resolves an extension by name, returning whether the server
// implements it and, if so, its major opcode. It is the standard gate before
// sending any extension request.
func queryExtension(r Requester, name string) (present bool, major byte, err error) {
	e := NewEncoder(r.Order())
	e.Put16(uint16(len(name)))
	e.Skip(2) // unused
	e.PutString(name)
	reply, err := r.Request("QueryExtension", opQueryExtension, 0, e.Bytes())
	if err != nil {
		return false, 0, err
	}
	hdr, _, err := splitReply("QueryExtension", reply)
	if err != nil {
		return false, 0, err
	}
	return hdr[8] != 0, hdr[9], nil
}

// internAtom resolves an atom by name without creating it: an atom nobody has
// ever interned comes back as AtomNone rather than as an error, which is
// exactly the answer wanted for a property no driver exports.
func internAtom(r Requester, name string) (uint32, error) {
	e := NewEncoder(r.Order())
	e.Put16(uint16(len(name)))
	e.Skip(2) // unused
	e.PutString(name)
	reply, err := r.Request("InternAtom", opInternAtom, 1, e.Bytes())
	if err != nil {
		return 0, err
	}
	hdr, _, err := splitReply("InternAtom", reply)
	if err != nil {
		return 0, err
	}
	return r.Order().Uint32(hdr[8:12]), nil
}

// getAtomName resolves an atom back to its name.
func getAtomName(r Requester, atom uint32) (string, error) {
	e := NewEncoder(r.Order())
	e.Put32(atom)
	reply, err := r.Request("GetAtomName", opGetAtomName, 0, e.Bytes())
	if err != nil {
		return "", err
	}
	hdr, extra, err := splitReply("GetAtomName", reply)
	if err != nil {
		return "", err
	}
	n := int(r.Order().Uint16(hdr[8:10]))
	if n > len(extra) {
		return "", fmt.Errorf("x11: GetAtomName: truncated name (%d of %d bytes)", len(extra), n)
	}
	return string(extra[:n]), nil
}

// Randr is a queried RANDR handle: the extension's major opcode plus the
// version the server agreed to speak.
type Randr struct {
	r        Requester
	major    byte
	VerMajor uint32
	VerMinor uint32
}

// QueryRandr queries RANDR and negotiates version 1.5, which is the one that
// carries RRGetMonitors. It returns (nil, nil) when the server has no RANDR,
// because "the server does not offer it" is an answer, not a failure.
func QueryRandr(r Requester) (*Randr, error) {
	present, major, err := queryExtension(r, RandrName)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	e := NewEncoder(r.Order())
	e.Put32(1)
	e.Put32(5)
	reply, err := r.Request("RRQueryVersion", major, rrReqQueryVersion, e.Bytes())
	if err != nil {
		return nil, err
	}
	hdr, _, err := splitReply("RRQueryVersion", reply)
	if err != nil {
		return nil, err
	}
	return &Randr{
		r:        r,
		major:    major,
		VerMajor: r.Order().Uint32(hdr[8:12]),
		VerMinor: r.Order().Uint32(hdr[12:16]),
	}, nil
}

// HasMonitors reports whether the negotiated version carries RRGetMonitors,
// which arrived in RANDR 1.5.
func (rr *Randr) HasMonitors() bool {
	return rr.VerMajor > 1 || (rr.VerMajor == 1 && rr.VerMinor >= 5)
}

// decodeMonitors parses the MONITORINFO list of an RRGetMonitors reply. Each
// entry is a fixed 24-byte head followed by ncrtcs 4-byte output ids.
func decodeMonitors(order ByteOrder, count int, body []byte) ([]Monitor, error) {
	d := NewDecoder(order, body)
	out := make([]Monitor, 0, count)
	for i := 0; i < count; i++ {
		var m Monitor
		m.NameAtom = d.Get32()
		m.Primary = d.Get8() != 0
		m.Automatic = d.Get8() != 0
		n := int(d.Get16())
		m.X = d.Get16s()
		m.Y = d.Get16s()
		m.Width = d.Get16()
		m.Height = d.Get16()
		m.WidthMM = d.Get32()
		m.HeightMM = d.Get32()
		if n > 0 {
			m.Outputs = make([]uint32, 0, n)
			for j := 0; j < n; j++ {
				m.Outputs = append(m.Outputs, d.Get32())
			}
		}
		if !d.OK() {
			return nil, fmt.Errorf("x11: RRGetMonitors: truncated monitor %d of %d", i, count)
		}
		out = append(out, m)
	}
	return out, nil
}

// GetMonitors lists the monitors of the screen rooted at root. Names are
// resolved from their atoms, so a monitor comes back as "HDMI-1" rather than
// as a number; models are NOT read here, because that costs one round trip per
// output and a caller that only wants rectangles should not pay for it. Use
// [Randr.ResolveModels] or [Monitors] for those.
func (rr *Randr) GetMonitors(root uint32) ([]Monitor, error) {
	e := NewEncoder(rr.r.Order())
	e.Put32(root)
	e.Put8(1) // get-active: only monitors currently driving pixels
	e.Skip(3)
	reply, err := rr.r.Request("RRGetMonitors", rr.major, rrReqGetMonitors, e.Bytes())
	if err != nil {
		return nil, err
	}
	hdr, extra, err := splitReply("RRGetMonitors", reply)
	if err != nil {
		return nil, err
	}
	n := int(rr.r.Order().Uint32(hdr[12:16]))
	mons, err := decodeMonitors(rr.r.Order(), n, extra)
	if err != nil {
		return nil, err
	}
	for i := range mons {
		if mons[i].NameAtom == AtomNone {
			continue
		}
		name, err := getAtomName(rr.r, mons[i].NameAtom)
		if err != nil {
			// A monitor whose name atom vanished between the two requests is
			// still a perfectly usable rectangle; it just stays nameless.
			continue
		}
		mons[i].Name = name
	}
	return mons, nil
}

// OutputProperty reads up to maxWords 32-bit words of a RANDR output
// property. A property the output does not have comes back with format 0 and
// no value, which is not an error.
func (rr *Randr) OutputProperty(output, property uint32, maxWords uint32) (format byte, value []byte, err error) {
	e := NewEncoder(rr.r.Order())
	e.Put32(output)
	e.Put32(property)
	e.Put32(0) // type: AnyPropertyType
	e.Put32(0) // long-offset
	e.Put32(maxWords)
	e.Put8(0) // delete
	e.Put8(0) // pending
	e.Skip(2) // unused
	reply, err := rr.r.Request("RRGetOutputProperty", rr.major, rrReqGetOutputProperty, e.Bytes())
	if err != nil {
		return 0, nil, err
	}
	hdr, extra, err := splitReply("RRGetOutputProperty", reply)
	if err != nil {
		return 0, nil, err
	}
	format = hdr[1]
	n := int(rr.r.Order().Uint32(hdr[16:20]))
	switch format {
	case 8:
		// n is already a byte count.
	case 16:
		n *= 2
	case 32:
		n *= 4
	default:
		return format, nil, nil // format 0: the output has no such property
	}
	if n > len(extra) {
		return 0, nil, fmt.Errorf("x11: RRGetOutputProperty: %d bytes announced, %d received",
			n, len(extra))
	}
	return format, extra[:n], nil
}

// edidMaxWords caps the EDID read at 32 blocks of 128 bytes. One block is the
// base EDID and is all this package parses; the cap exists so a driver
// exporting a large extension chain cannot make the reply unbounded.
const edidMaxWords = 32 * 128 / 4

// ResolveModels fills in the Model of each monitor from its outputs' EDID.
//
// It is best-effort by construction: an output with no EDID property, a
// server that refuses the request, and a blob that is not an EDID all leave
// Model empty rather than failing the enumeration — the rectangles are still
// right, and a caller that wanted them should not lose them because a display
// declines to introduce itself. It reports an error only when the EDID atom
// itself cannot be looked up, which means the connection is in trouble.
func (rr *Randr) ResolveModels(mons []Monitor) error {
	edid, err := internAtom(rr.r, "EDID")
	if err != nil {
		return err
	}
	if edid == AtomNone {
		// No client and no driver has ever interned "EDID" on this server, so
		// no output can be carrying the property. Nothing to ask.
		return nil
	}
	for i := range mons {
		for _, out := range mons[i].Outputs {
			format, value, err := rr.OutputProperty(out, edid, edidMaxWords)
			if err != nil || format != 8 {
				continue
			}
			if name := EDIDModelName(value); name != "" {
				mons[i].Model = name
				break
			}
		}
	}
	return nil
}

// edidHeader is the fixed 8-byte signature a base EDID block starts with. It
// is checked before anything is read out of the blob, so a property that
// happens to be format 8 but is not an EDID yields no name rather than
// nonsense.
var edidHeader = [8]byte{0x00, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x00}

// EDID base-block layout (VESA E-EDID): four 18-byte descriptors, the first at
// offset 54. A descriptor whose first two bytes are zero is a DISPLAY
// descriptor rather than a detailed timing, and its tag byte says which kind;
// 0xfc is the display's product name.
const (
	edidBlockLen      = 128
	edidDescriptorOff = 54
	edidDescriptorLen = 18
	edidDescriptors   = 4
	edidTagName       = 0xfc
)

// EDIDModelName returns the display product name carried by a base EDID block
// — the string a user recognises, "DELL U2720Q" rather than "DP-2" — or "" if
// the blob is not an EDID or carries no name descriptor.
//
// The name is a 13-byte field terminated by a line feed and padded with
// spaces, so both the terminator and the padding are trimmed. Only the base
// block is read: the name descriptor is required to be there, and an extension
// block cannot move it.
func EDIDModelName(edid []byte) string {
	if len(edid) < edidBlockLen {
		return ""
	}
	if [8]byte(edid[:8]) != edidHeader {
		return ""
	}
	for i := 0; i < edidDescriptors; i++ {
		d := edid[edidDescriptorOff+i*edidDescriptorLen:][:edidDescriptorLen]
		// d[0:2] is a detailed timing's pixel clock; zero marks a display
		// descriptor. d[2] and d[4] are reserved zero bytes, d[3] is the tag.
		if d[0] != 0 || d[1] != 0 || d[3] != edidTagName {
			continue
		}
		if name := trimEDIDText(d[5:]); name != "" {
			return name
		}
	}
	return ""
}

// trimEDIDText cuts an EDID text field at its line-feed terminator and strips
// the space padding that follows a short name.
func trimEDIDText(b []byte) string {
	end := len(b)
	for i, c := range b {
		if c == '\n' {
			end = i
			break
		}
	}
	for end > 0 && b[end-1] == ' ' {
		end--
	}
	return string(b[:end])
}

// Xinerama is a queried XINERAMA handle.
type Xinerama struct {
	r        Requester
	major    byte
	VerMajor uint16
	VerMinor uint16
}

// QueryXinerama queries XINERAMA. It returns (nil, nil) when the server has
// none.
func QueryXinerama(r Requester) (*Xinerama, error) {
	present, major, err := queryExtension(r, XineramaName)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	e := NewEncoder(r.Order())
	e.Put8(1)
	e.Put8(1)
	e.Skip(2)
	reply, err := r.Request("XineramaQueryVersion", major, xinReqQueryVersion, e.Bytes())
	if err != nil {
		return nil, err
	}
	hdr, _, err := splitReply("XineramaQueryVersion", reply)
	if err != nil {
		return nil, err
	}
	return &Xinerama{
		r:        r,
		major:    major,
		VerMajor: r.Order().Uint16(hdr[8:10]),
		VerMinor: r.Order().Uint16(hdr[10:12]),
	}, nil
}

// decodeXineramaScreens parses the ScreenInfo list of a QueryScreens reply:
// count entries of four INT16/CARD16 fields each.
func decodeXineramaScreens(order ByteOrder, count int, body []byte) ([]Monitor, error) {
	d := NewDecoder(order, body)
	out := make([]Monitor, 0, count)
	for i := 0; i < count; i++ {
		m := Monitor{
			X:      d.Get16s(),
			Y:      d.Get16s(),
			Width:  d.Get16(),
			Height: d.Get16(),
		}
		if !d.OK() {
			return nil, fmt.Errorf("x11: XineramaQueryScreens: truncated screen %d of %d", i, count)
		}
		out = append(out, m)
	}
	// XINERAMA carries no primary flag; the first screen is the one the
	// window manager treats as primary in practice.
	if len(out) > 0 {
		out[0].Primary = true
	}
	return out, nil
}

// QueryScreens lists the Xinerama screens, which are the same rectangles
// RANDR would call monitors, without names.
func (x *Xinerama) QueryScreens() ([]Monitor, error) {
	reply, err := x.r.Request("XineramaQueryScreens", x.major, xinReqQueryScreens, nil)
	if err != nil {
		return nil, err
	}
	hdr, extra, err := splitReply("XineramaQueryScreens", reply)
	if err != nil {
		return nil, err
	}
	n := int(x.r.Order().Uint32(hdr[8:12]))
	return decodeXineramaScreens(x.r.Order(), n, extra)
}

// Monitors lists the monitors of screen sc, trying RANDR 1.5 first — with the
// displays' own model names filled in where they publish an EDID — then
// XINERAMA, and falling back to the whole screen as a single nameless monitor.
//
// It never returns an empty list without an error: a screen always has at
// least itself. An extension that is absent, that refuses, or that answers
// nothing is not an error either; it just means the next way of asking gets a
// turn. The one error is a screen that does not exist.
func Monitors(r Requester, sc *Screen) ([]Monitor, error) {
	if sc == nil {
		return nil, fmt.Errorf("x11: cannot enumerate the monitors of a nil screen")
	}
	if rr, err := QueryRandr(r); err == nil && rr != nil && rr.HasMonitors() {
		if mons, err := rr.GetMonitors(sc.Root); err == nil && len(mons) > 0 {
			// The rectangles are already right; a failure here costs the
			// model names and nothing else.
			_ = rr.ResolveModels(mons)
			return mons, nil
		}
	}
	if xin, err := QueryXinerama(r); err == nil && xin != nil {
		if mons, err := xin.QueryScreens(); err == nil && len(mons) > 0 {
			return mons, nil
		}
	}
	return []Monitor{{
		Primary:  true,
		Width:    sc.Width,
		Height:   sc.Height,
		WidthMM:  uint32(sc.WidthMM),
		HeightMM: uint32(sc.HeightMM),
	}}, nil
}
