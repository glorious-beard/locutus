package runner

import "testing"

func TestDispatchUsesOuterLoop(t *testing.T) {
	if dispatchUsesOuterLoop("claude-code") {
		t.Fatal("claude-code must NOT use the harness outer loop (DJ-144): the dynamic workflow owns the loop")
	}
	for _, rt := range []string{"codex", "gemini"} {
		if !dispatchUsesOuterLoop(rt) {
			t.Fatalf("%s must keep the harness outer loop (DJ-142 driver unchanged)", rt)
		}
	}
}
