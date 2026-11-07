package main

import "testing"

// RETAIN_DAYS keeps its meaning: what is written as a whole number is the
// days, so zero or below keeps everything, and only what is unset or not a
// number takes the 730 days.
func TestRetainDays(t *testing.T) {
	for _, c := range []struct {
		env  string
		want int
	}{
		{"", 730}, {"30", 30}, {"0", 0}, {"-1", -1}, {"abc", 730}, {"30.5", 730}, {" 30", 730},
	} {
		t.Setenv("RETAIN_DAYS", c.env)
		if got := retainDays(); got != c.want {
			t.Errorf("RETAIN_DAYS=%q: %d, want %d", c.env, got, c.want)
		}
	}
}
