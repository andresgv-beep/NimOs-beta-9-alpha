package main

import "testing"

// Caracterización de la función pura de db.go antes de modularizar.
// (El resto de db.go toca SQLite y se valida con el daemon arrancado.)
func TestJoinStrings_Characterization(t *testing.T) {
	cases := []struct {
		parts []string
		sep   string
		want  string
	}{
		{nil, ",", ""},
		{[]string{}, ",", ""},
		{[]string{"a"}, ",", "a"},
		{[]string{"a", "b", "c"}, ", ", "a, b, c"},
		{[]string{"x", "y"}, "", "xy"},
		{[]string{"col = ?", "role = ?"}, ", ", "col = ?, role = ?"},
	}
	for _, c := range cases {
		if got := joinStrings(c.parts, c.sep); got != c.want {
			t.Errorf("joinStrings(%v,%q) = %q, want %q", c.parts, c.sep, got, c.want)
		}
	}
}
