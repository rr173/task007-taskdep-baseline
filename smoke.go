package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
)

// runSmokeTest exercises the core service and the HTTP layer end-to-end without
// any external dependency. It returns a non-nil error (causing exit 1) on the
// first failed assertion.
func runSmokeTest() error {
	if err := smokeCore(); err != nil {
		return fmt.Errorf("core: %w", err)
	}
	if err := smokeHTTP(); err != nil {
		return fmt.Errorf("http: %w", err)
	}
	return nil
}

func smokeCore() error {
	// Scenario 1: a linear DAG with a free-floating task, exercising stable
	// ordering and the completion gate.
	s := NewService()
	a, err := s.Create("A", nil)
	if err != nil {
		return fmt.Errorf("create A: %w", err)
	}
	if a.Status != StatusReady {
		return fmt.Errorf("A status = %s, want ready", a.Status)
	}
	if _, err := s.Create("B", []string{"A"}); err != nil {
		return fmt.Errorf("create B: %w", err)
	}
	if _, err := s.Create("C", []string{"A", "B"}); err != nil {
		return fmt.Errorf("create C: %w", err)
	}
	if _, err := s.Create("D", nil); err != nil {
		return fmt.Errorf("create D: %w", err)
	}
	b, _ := s.Get("B")
	if b.Status != StatusPending {
		return fmt.Errorf("B status = %s, want pending", b.Status)
	}
	order, err := s.Order()
	if err != nil {
		return fmt.Errorf("order: %w", err)
	}
	if want := []string{"A", "B", "C", "D"}; !equalSlices(order, want) {
		return fmt.Errorf("order = %v, want %v", order, want)
	}
	if _, err := s.Complete("A"); err != nil {
		return fmt.Errorf("complete A: %w", err)
	}
	b, _ = s.Get("B")
	if b.Status != StatusReady {
		return fmt.Errorf("after A done, B status = %s, want ready", b.Status)
	}
	if _, err := s.Complete("C"); err == nil {
		return errors.New("completing C before B should fail")
	}
	if _, err := s.Complete("B"); err != nil {
		return fmt.Errorf("complete B: %w", err)
	}
	if _, err := s.Complete("C"); err != nil {
		return fmt.Errorf("complete C: %w", err)
	}
	if _, err := s.Complete("A"); err == nil {
		return errors.New("completing A twice should fail")
	}
	if _, err := s.Complete("D"); err != nil {
		return fmt.Errorf("complete D: %w", err)
	}

	// Scenario 2: a cycle between two tasks; the order endpoint must report a
	// cycle path naming both.
	s2 := NewService()
	if _, err := s2.Create("X", []string{"Y"}); err != nil {
		return fmt.Errorf("create X: %w", err)
	}
	if _, err := s2.Create("Y", []string{"X"}); err != nil {
		return fmt.Errorf("create Y: %w", err)
	}
	_, err = s2.Order()
	if err == nil {
		return errors.New("cyclic order should fail")
	}
	var ce *cycleErr
	if !errors.As(err, &ce) {
		return fmt.Errorf("want cycleErr, got %T: %v", err, err)
	}
	if !containsAll(ce.path, "X", "Y") {
		return fmt.Errorf("cycle path %v should name X and Y", ce.path)
	}

	// Scenario 3: a task may not depend on itself.
	s3 := NewService()
	if _, err := s3.Create("Z", []string{"Z"}); err == nil {
		return errors.New("self-dependency should be rejected")
	}

	// Scenario 4: a forward dependency that is never registered makes the
	// graph incomplete; ordering must list the dangling name.
	s4 := NewService()
	if _, err := s4.Create("P", []string{"Q"}); err != nil {
		return fmt.Errorf("create P with forward dep: %w", err)
	}
	_, err = s4.Order()
	if err == nil {
		return errors.New("dangling-dependency order should fail")
	}
	var me *missingErr
	if !errors.As(err, &me) {
		return fmt.Errorf("want missingErr, got %T: %v", err, err)
	}
	if !containsAll(me.names, "Q") {
		return fmt.Errorf("missing list %v should contain Q", me.names)
	}

	// Scenario 5: duplicate name and completing a missing task.
	if _, err := s4.Create("P", nil); err == nil {
		return errors.New("duplicate task name should be rejected")
	}
	if _, err := s4.Complete("nope"); err == nil {
		return errors.New("completing a missing task should fail")
	}

	return nil
}

func smokeHTTP() error {
	srv := NewService()
	ts := httptest.NewServer(buildMux(srv))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		return fmt.Errorf("healthz: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	resp, err = http.Post(ts.URL+"/tasks", "application/json", bytes.NewBufferString(`{"name":"A"}`))
	if err != nil {
		return fmt.Errorf("post tasks: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("post tasks status = %d, want 201", resp.StatusCode)
	}
	resp.Body.Close()

	resp, err = http.Get(ts.URL + "/order")
	if err != nil {
		return fmt.Errorf("get order: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("order status = %d, want 200", resp.StatusCode)
	}
	var res struct {
		Order []string `json:"order"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return fmt.Errorf("decode order: %w", err)
	}
	if want := []string{"A"}; !equalSlices(res.Order, want) {
		return fmt.Errorf("order = %v, want %v", res.Order, want)
	}

	resp, err = http.Post(ts.URL+"/tasks", "application/json", bytes.NewBufferString(`{bad`))
	if err != nil {
		return fmt.Errorf("post bad json: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("bad json status = %d, want 400", resp.StatusCode)
	}

	return nil
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsAll(s []string, want ...string) bool {
	set := make(map[string]bool, len(s))
	for _, v := range s {
		set[v] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}
