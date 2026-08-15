package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Regression probe for BUG1: when a task is created without dependencies,
// the JSON representation of its DependsOn field must be an empty array [],
// not null.
func TestBug1_DependsOnEmptyArray(t *testing.T) {
	srv := NewService()
	ts := httptest.NewServer(buildMux(srv))
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/tasks", "application/json",
		bytes.NewBufferString(`{"name":"solo"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw json.RawMessage
	json.NewDecoder(resp.Body).Decode(&raw)
	if bytes.Contains(raw, []byte(`"dependsOn":null`)) {
		t.Fatal("task with no dependencies should have dependsOn:[] in JSON, got null")
	}
}

// Regression probe for BUG2: the Get method should trim whitespace from the
// task name before lookup, consistent with Create.
func TestBug2_GetTrimsWhitespace(t *testing.T) {
	s := NewService()
	s.Create("alpha", nil)
	_, err := s.Get(" alpha ")
	if err != nil {
		t.Fatalf("Get with leading/trailing spaces should find the task: %v", err)
	}
}

// Regression probe for BUG3: the Complete method should trim whitespace from
// the task name before lookup, consistent with Create.
func TestBug3_CompleteTrimsWhitespace(t *testing.T) {
	s := NewService()
	s.Create("beta", nil)
	_, err := s.Complete(" beta ")
	if err != nil {
		t.Fatalf("Complete with leading/trailing spaces should find the task: %v", err)
	}
}

// Regression probe for BUG4: when a dependency cycle is detected, the reported
// cycle path must follow the dependency direction. For A depends-on B,
// B depends-on C, C depends-on A, a valid path is [A,B,C,A] (or any rotation).
func TestBug4_CyclePathFollowsDependencyDirection(t *testing.T) {
	s := NewService()
	// A depends on B, B depends on C, C depends on A
	s.Create("A", []string{"B"})
	s.Create("B", []string{"C"})
	s.Create("C", []string{"A"})
	_, err := s.Order()
	if err == nil {
		t.Fatal("expected cycle error")
	}
	ce, ok := err.(*cycleErr)
	if !ok {
		t.Fatalf("expected cycleErr, got %T: %v", err, err)
	}
	if len(ce.path) < 3 {
		t.Fatalf("cycle path too short: %v", ce.path)
	}
	// Build a dependency map for quick lookup
	deps := map[string]string{"A": "B", "B": "C", "C": "A"}
	// Each consecutive pair in the path must represent a "depends on" edge
	for i := 0; i < len(ce.path)-1; i++ {
		from := ce.path[i]
		to := ce.path[i+1]
		if deps[from] != to {
			t.Fatalf("cycle path %v does not follow dependency edges at position %d: "+
				"%s does not depend on %s", ce.path, i, from, to)
		}
	}
}

// Regression probe for BUG5: Create should reject dependency names that contain
// the '/' character, because task names with '/' are forbidden and such
// dependencies can never be resolved.
func TestBug5_RejectSlashInDepName(t *testing.T) {
	s := NewService()
	_, err := s.Create("gamma", []string{"invalid/dep"})
	if err == nil {
		t.Fatal("Create should reject dependency names containing '/'")
	}
}
