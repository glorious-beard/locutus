package spec

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	reNonAlnum    = regexp.MustCompile(`[^a-z0-9]+`)
	reMultiDash   = regexp.MustCompile(`-{2,}`)
)

// SlugID derives a kebab-case slug from a title.
// "OAuth Login via Google" → "oauth-login-via-google"
// Truncates at a word boundary to keep the slug <= 50 chars.
func SlugID(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = reNonAlnum.ReplaceAllString(s, "-")
	s = reMultiDash.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")

	if len(s) > 50 {
		full := s
		s = full[:50]
		// Only trim to word boundary if the cut lands mid-word.
		if full[50] != '-' {
			if idx := strings.LastIndex(s, "-"); idx > 0 {
				s = s[:idx]
			}
		}
		s = strings.Trim(s, "-")
	}

	return s
}

// UniqueID appends a 6-char hex suffix for collision resolution.
// The suffix is the first 6 chars of SHA-256(title + createdAt.RFC3339Nano).
func UniqueID(title string, createdAt time.Time) string {
	slug := SlugID(title)
	h := sha256.Sum256([]byte(title + createdAt.Format(time.RFC3339Nano)))
	suffix := fmt.Sprintf("%x", h)[:6]
	return slug + "-" + suffix
}
