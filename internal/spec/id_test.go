package spec

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSlugID(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple", "OAuth Login via Google", "oauth-login-via-google"},
		{"all caps", "REST API Gateway", "rest-api-gateway"},
		{"special chars", "Auth (OAuth2) & JWT", "auth-oauth2-jwt"},
		{"extra spaces", "  user   login  ", "user-login"},
		{"numbers", "HTTP2 Over TLS", "http2-over-tls"},
		{"leading trailing dashes", "---feature---", "feature"},
		{"long title truncates at word boundary", "A very long title that exceeds the fifty character limit set for slugs", "a-very-long-title-that-exceeds-the-fifty-character"},
		{"already short", "Login", "login"},
		{"single word", "Authentication", "authentication"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SlugID(tt.input)
			assert.Equal(t, tt.want, got)
			assert.True(t, len(got) <= 50, "slug should be <= 50 chars, got %d", len(got))
			assert.False(t, strings.HasPrefix(got, "-"), "slug should not start with dash")
			assert.False(t, strings.HasSuffix(got, "-"), "slug should not end with dash")
		})
	}
}

func TestUniqueID(t *testing.T) {
	title := "OAuth Login via Google"
	t0 := time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC)

	// Deterministic for same inputs.
	id1 := UniqueID(title, t0)
	id2 := UniqueID(title, t0)
	assert.Equal(t, id1, id2, "UniqueID should be deterministic for same inputs")

	// Has slug prefix.
	assert.True(t, strings.HasPrefix(id1, "oauth-login-via-google-"), "should have slug prefix")

	// Has 6-char suffix.
	parts := strings.Split(id1, "-")
	suffix := parts[len(parts)-1]
	assert.Len(t, suffix, 6, "suffix should be 6 chars")

	// Differs for different timestamps.
	t1 := t0.Add(time.Nanosecond)
	id3 := UniqueID(title, t1)
	assert.NotEqual(t, id1, id3, "UniqueID should differ for different timestamps")

	// Differs for different titles.
	id4 := UniqueID("Different Title", t0)
	assert.NotEqual(t, id1, id4, "UniqueID should differ for different titles")
}
