package cmd

import (
	"strings"
	"testing"
)

func TestInjectMaxIterations(t *testing.T) {
	body := "Run the workflow with cap {{max_iterations}} iterations."
	got := injectMaxIterations(body, 12)
	if !strings.Contains(got, "cap 12 iterations") {
		t.Fatalf("token not substituted: %q", got)
	}
	if strings.Contains(got, "{{max_iterations}}") {
		t.Fatal("token left in body")
	}
	// No token → unchanged.
	plain := "no token here"
	if injectMaxIterations(plain, 12) != plain {
		t.Fatal("body without token must be unchanged")
	}
}
