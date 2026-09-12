// Package indiserve multiplexes supervised children to INDI clients on a TCP port.
package indiserve

import (
	"context"
	"fmt"
	"net"
	"sort"
	"sync"

	"github.com/mikefsq/indihurd/internal/indiwire"
	"github.com/mikefsq/indihurd/internal/snapshot"
)

// Child provides a supervised device snapshot and property writes.
type Child interface {
	Snapshot() *snapshot.Snapshot
	SetNumber(ctx context.Context, device, prop string, values map[string]float64) error
	SetSwitch(ctx context.Context, device, prop string, on, off []string) error
	SetText(ctx context.Context, device, prop string, values map[string]string) error
}

// Server multiplexes one or more children to INDI clients on a TCP port.
type Server struct {
	addr       string
	children   []Child
	childrenMu sync.RWMutex
	logf       func(string, ...any)

	mu        sync.Mutex
	conns     map[*conn]struct{}
	ln        net.Listener
	dupWarned map[string]bool
}

// New builds a server over children, served in the order given.
func New(addr string, logf func(string, ...any), children ...Child) *Server {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Server{addr: addr, children: children, logf: logf, conns: map[*conn]struct{}{}, dupWarned: map[string]bool{}}
}

// SetChildren updates routing without disconnecting unrelated INDI clients.
func (s *Server) SetChildren(children []Child) {
	s.childrenMu.Lock()
	s.children = append([]Child(nil), children...)
	s.childrenMu.Unlock()
}
func (s *Server) Children() []Child {
	s.childrenMu.RLock()
	defer s.childrenMu.RUnlock()
	return append([]Child(nil), s.children...)
}

// Serve listens until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("indiserve: listen %s: %w", s.addr, err)
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.ln = nil
		s.mu.Unlock()
	}()
	go func() { <-ctx.Done(); ln.Close() }()
	s.logf("indiserve: listening on %s", ln.Addr())

	for {
		nc, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				s.closeAll()
				return nil
			}
			return err
		}
		c := newConn(nc, s)
		s.mu.Lock()
		s.conns[c] = struct{}{}
		s.mu.Unlock()
		go c.writeLoop()
		go c.serve(ctx)
	}
}

// Addr is the bound address, nil until Serve has listened.
func (s *Server) Addr() net.Addr {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return nil
	}
	return s.ln.Addr()
}

func (s *Server) closeAll() {
	s.mu.Lock()
	for c := range s.conns {
		c.close()
	}
	s.mu.Unlock()
}

func (s *Server) drop(c *conn) {
	s.mu.Lock()
	delete(s.conns, c)
	s.mu.Unlock()
	c.close()
}

func (s *Server) dropf(c *conn, format string, args ...any) {
	s.logf("indiserve: client %s dropped: %v", c.nc.RemoteAddr(), fmt.Sprintf(format, args...))
	s.drop(c)
}

// Publish fans one driver element out to every interested client, serialising
// it before returning because indiwire reuses the element's backing array.
func (s *Server) Publish(el *indiwire.Element) {
	// A BLOB set's payload travels out of band, so forwarding the bare
	// metadata here would deliver empty frames. PublishBlob carries it.
	if el.Kind == indiwire.KindSet && el.Type == indiwire.BLOB {
		return
	}
	s.publish(el, nil)
}

// PublishBlob is Publish for a BLOB set whose bytes arrived out of band, keyed
// by member name and valid only for this call.
func (s *Server) PublishBlob(el *indiwire.Element, data map[string][]byte) {
	s.publish(el, data)
}

func (s *Server) publish(el *indiwire.Element, blobs map[string][]byte) {
	s.mu.Lock()
	targets := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		targets = append(targets, c)
	}
	s.mu.Unlock()
	msg := "" // encoded lazily: a BLOB is megabytes and most conns may not want it
	for _, c := range targets {
		if !c.wants(el) {
			continue
		}
		if msg == "" {
			var err error
			if blobs != nil {
				msg, err = indiwire.EncodeBlobElement(el, blobs)
			} else {
				msg, err = indiwire.EncodeElement(el)
			}
			if err != nil {
				s.logf("indiserve: cannot encode %s.%s: %v", el.Device, el.Name, err)
				return
			}
		}
		if err := c.enqueue(msg); err != nil {
			s.dropf(c, "%v", err)
		}
	}
}

func (s *Server) devices() []string {
	seen := map[string]bool{}
	for _, ch := range s.Children() {
		snap := ch.Snapshot()
		if !snap.Valid() {
			continue
		}
		for _, d := range snap.Devices() {
			if seen[d] {
				s.warnDup(d)
			}
			seen[d] = true
		}
	}
	out := make([]string, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// childFor returns the first child publishing device.
func (s *Server) childFor(device string) Child {
	var found Child
	for _, ch := range s.Children() {
		snap := ch.Snapshot()
		if !snap.Valid() {
			continue
		}
		for _, d := range snap.Devices() {
			if d == device {
				if found == nil {
					found = ch
				} else {
					s.warnDup(device)
				}
			}
		}
	}
	return found
}

// warnDup logs each duplicate device name once.
func (s *Server) warnDup(device string) {
	s.mu.Lock()
	first := !s.dupWarned[device]
	s.dupWarned[device] = true
	s.mu.Unlock()
	if first {
		s.logf("indiserve: device %q is published by more than one child; routing and replay use the first", device)
	}
}

// replay sends cached definitions to a client requesting properties.
func (s *Server) replay(c *conn, device, name string) {
	for _, ch := range s.Children() {
		snap := ch.Snapshot()
		if !snap.Valid() {
			continue
		}
		for _, dev := range snap.Devices() {
			if device != "" && dev != device {
				continue
			}
			props := snap.Properties(dev)
			sort.Strings(props)
			for _, prop := range props {
				if name != "" && prop != name {
					continue
				}
				v, ok := snap.Vector(dev, prop)
				if !ok {
					continue
				}
				el := vectorToElement(v)
				if !c.wants(el) {
					continue
				}
				msg, err := indiwire.EncodeElement(el)
				if err != nil {
					s.logf("indiserve: cannot encode %s.%s: %v", dev, prop, err)
					continue
				}
				if err := c.enqueue(msg); err != nil {
					s.dropf(c, "%v", err)
					return
				}
			}
		}
	}
}

func vectorToElement(v *snapshot.Vector) *indiwire.Element {
	el := &indiwire.Element{
		Kind: indiwire.KindDef, Type: v.Type, Rule: v.Rule, Perm: v.Perm, State: v.State,
		Device: v.Device, Name: v.Name, Label: v.Label, Group: v.Group, Timestamp: v.Timestamp,
	}
	for _, m := range v.Members {
		el.Members = append(el.Members, m.Member)
	}
	return el
}

func (s *Server) forward(ctx context.Context, el *indiwire.Element) error {
	ch := s.childFor(el.Device)
	if ch == nil {
		return fmt.Errorf("no such device %q", el.Device)
	}
	switch el.Type {
	case indiwire.Number:
		vals := map[string]float64{}
		for _, m := range el.Members {
			vals[m.Name] = m.Value
		}
		return ch.SetNumber(ctx, el.Device, el.Name, vals)
	case indiwire.Switch:
		var on, off []string
		for _, m := range el.Members {
			if m.On {
				on = append(on, m.Name)
			} else {
				off = append(off, m.Name)
			}
		}
		return ch.SetSwitch(ctx, el.Device, el.Name, on, off)
	case indiwire.Text:
		vals := map[string]string{}
		for _, m := range el.Members {
			vals[m.Name] = m.Text
		}
		return ch.SetText(ctx, el.Device, el.Name, vals)
	}
	return fmt.Errorf("cannot write a %v vector", el.Type)
}
