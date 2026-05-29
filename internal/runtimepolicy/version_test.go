package runtimepolicy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBelowFloor(t *testing.T) {
	cases := []struct {
		name     string
		detected string
		floor    string
		below    bool
		ok       bool // false when comparison is indeterminate (fail-open)
	}{
		{"clearly below", "2.1.100", "2.1.154", true, true},
		{"clearly above", "2.2.0", "2.1.154", false, true},
		{"equal", "2.1.154", "2.1.154", false, true},
		{"patch below", "2.1.153", "2.1.154", true, true},
		{"prefixed v", "v2.1.200", "2.1.154", false, true},
		{"trailing label", "2.1.154-beta.1", "2.1.154", false, true},
		{"unparseable detected", "weird-build", "2.1.154", false, false},
		{"empty detected", "", "2.1.154", false, false},
		{"empty floor", "2.1.0", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			below, ok := BelowFloor(c.detected, c.floor)
			assert.Equal(t, c.ok, ok, "ok mismatch")
			if c.ok {
				assert.Equal(t, c.below, below, "below mismatch")
			}
		})
	}
}
