package main

import (
	"net/http/httptest"
	"testing"
)

// TestCheckRequestPayload_PercentEncodingBypass is the closure test for the
// NimShield percent-encoding bypass: an encoded XSS/SQLi payload in the query
// string must be detected, not just the literal form.
func TestCheckRequestPayload_PercentEncodingBypass(t *testing.T) {
	cases := []struct {
		name     string
		rawQuery string
		wantRule string // "" means: must be detected (non-empty rule)
	}{
		{"literal xss", "q=<script>alert(1)</script>", "CSP-001"},
		{"encoded xss", "q=%3Cscript%3Ealert(1)%3C%2Fscript%3E", "CSP-001"},
		{"double encoded xss", "q=%253Cscript%253E", "CSP-001"},
		{"encoded traversal in query", "f=%2e%2e%2f%2e%2e%2fetc%2fpasswd", ""}, // any rule is fine
		{"clean query", "page=2&sort=name", "OK"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/x?"+c.rawQuery, nil)
			got := checkRequestPayload(r)
			switch c.wantRule {
			case "OK":
				if got != "" {
					t.Errorf("clean query flagged as %q", got)
				}
			case "":
				if got == "" {
					t.Errorf("expected detection for %q, got none", c.rawQuery)
				}
			default:
				if got == "" {
					t.Errorf("expected %q for %q, got none", c.wantRule, c.rawQuery)
				}
			}
		})
	}
}

// TestCheckRequestPayload_EncodedPathTraversal ensures encoded traversal in the
// path is caught.
func TestCheckRequestPayload_EncodedPathTraversal(t *testing.T) {
	r := httptest.NewRequest("GET", "/files/%2e%2e/%2e%2e/etc/passwd", nil)
	if got := checkRequestPayload(r); got == "" {
		t.Errorf("expected traversal detection, got none (path=%q)", r.URL.Path)
	}
}

// TestCollectQueryForms verifies decoding layers and dedup.
func TestCollectQueryForms(t *testing.T) {
	if got := collectQueryForms(""); got != nil {
		t.Errorf("empty query should yield nil, got %v", got)
	}
	// A plain query with no encoding yields a single form.
	if got := collectQueryForms("a=1"); len(got) != 1 {
		t.Errorf("plain query should yield 1 form, got %d (%v)", len(got), got)
	}
	// Encoded query yields raw + decoded.
	got := collectQueryForms("q=%3Cb%3E")
	if len(got) < 2 {
		t.Errorf("encoded query should yield raw+decoded, got %v", got)
	}
}
