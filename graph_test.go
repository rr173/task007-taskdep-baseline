package main

import (
	"errors"
	"testing"
)

func TestCreateValidation(t *testing.T) {
	s := NewService()
	if _, err := s.Create("  ", nil); err == nil {
		t.Fatal("empty name should fail")
	}
	if _, err := s.Create("A/B", nil); err == nil {
		t.Fatal("name containing '/' should fail")
	}
	if _, err := s.Create("A", nil); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, err := s.Create("A", nil); err == nil {
		t.Fatal("duplicate name should fail")
	}
	if _, err := s.Create("B", []string{"B"}); err == nil {
		t.Fatal("self-dependency should fail")
	}
	// duplicate dependency names are deduped, not rejected
	c, err := s.Create("C", []string{"A", "A", " A "})
	if err != nil {
		t.Fatalf("create C with dup deps: %v", err)
	}
	if len(c.DependsOn) != 1 || c.DependsOn[0] != "A" {
		t.Fatalf("deps not deduped: %v", c.DependsOn)
	}
}

func TestStatusAndComplete(t *testing.T) {
	s := NewService()
	s.Create("A", nil)
	s.Create("B", []string{"A"})
	if b, _ := s.Get("B"); b.Status != StatusPending {
		t.Fatalf("B = %s, want pending", b.Status)
	}
	if _, err := s.Complete("B"); err == nil {
		t.Fatal("complete B before A should fail")
	}
	if _, err := s.Complete("missing"); err == nil {
		t.Fatal("completing a missing task should fail")
	}
	if _, err := s.Complete("A"); err != nil {
		t.Fatalf("complete A: %v", err)
	}
	if b, _ := s.Get("B"); b.Status != StatusReady {
		t.Fatalf("B = %s, want ready", b.Status)
	}
	if _, err := s.Complete("B"); err != nil {
		t.Fatalf("complete B: %v", err)
	}
	if b, _ := s.Get("B"); b.Status != StatusDone {
		t.Fatalf("B = %s, want done", b.Status)
	}
	if _, err := s.Complete("B"); err == nil {
		t.Fatal("completing B twice should fail")
	}
}

func TestOrderLinear(t *testing.T) {
	s := NewService()
	s.Create("A", nil)
	s.Create("B", []string{"A"})
	s.Create("C", []string{"A", "B"})
	s.Create("D", nil)
	order, err := s.Order()
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if want := []string{"A", "B", "C", "D"}; !equalSlices(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func TestOrderStableTieBreak(t *testing.T) {
	// Two independent roots: the earlier-registered one must precede the later
	// one even though both are always ready.
	s := NewService()
	s.Create("first", nil)
	s.Create("second", nil)
	order, err := s.Order()
	if err != nil {
		t.Fatalf("order: %v", err)
	}
	if want := []string{"first", "second"}; !equalSlices(order, want) {
		t.Fatalf("order = %v, want %v (stable registration order)", order, want)
	}
}

func TestOrderCycle(t *testing.T) {
	s := NewService()
	s.Create("X", []string{"Y"})
	s.Create("Y", []string{"X"})
	_, err := s.Order()
	if err == nil {
		t.Fatal("expected cycle error")
	}
	var ce *cycleErr
	if !errors.As(err, &ce) {
		t.Fatalf("expected cycleErr, got %T", err)
	}
	if !containsAll(ce.path, "X", "Y") {
		t.Fatalf("cycle path %v missing X/Y", ce.path)
	}
}

func TestOrderLargerCycle(t *testing.T) {
	s := NewService()
	s.Create("A", []string{"C"})
	s.Create("B", []string{"A"})
	s.Create("C", []string{"B"})
	_, err := s.Order()
	if err == nil {
		t.Fatal("expected cycle error")
	}
	var ce *cycleErr
	if !errors.As(err, &ce) {
		t.Fatalf("expected cycleErr, got %T", err)
	}
	if !containsAll(ce.path, "A", "B", "C") {
		t.Fatalf("cycle path %v should name A, B, C", ce.path)
	}
}

func TestOrderMissing(t *testing.T) {
	s := NewService()
	s.Create("P", []string{"Q"})
	_, err := s.Order()
	if err == nil {
		t.Fatal("expected missing error")
	}
	var me *missingErr
	if !errors.As(err, &me) {
		t.Fatalf("expected missingErr, got %T", err)
	}
	if !containsAll(me.names, "Q") {
		t.Fatalf("missing %v should contain Q", me.names)
	}
}

func TestListFilterAndOrder(t *testing.T) {
	s := NewService()
	s.Create("A", nil)          // ready
	s.Create("B", []string{"A"}) // pending

	all := s.List("")
	if len(all) != 2 {
		t.Fatalf("list len = %d, want 2", len(all))
	}
	if all[0].Name != "A" || all[1].Name != "B" {
		t.Fatalf("list not sorted by registration: %v %v", all[0].Name, all[1].Name)
	}
	if ready := s.List(StatusReady); len(ready) != 1 || ready[0].Name != "A" {
		t.Fatalf("ready = %v, want [A]", ready)
	}
	if pending := s.List(StatusPending); len(pending) != 1 || pending[0].Name != "B" {
		t.Fatalf("pending = %v, want [B]", pending)
	}
}

func TestCloneIsolation(t *testing.T) {
	s := NewService()
	created, _ := s.Create("A", []string{"X"})
	created.Status = StatusDone // mutate the value returned by Create
	got, _ := s.Get("A")
	if got.Status == StatusDone {
		t.Fatal("mutating a returned task affected the stored task")
	}
}
