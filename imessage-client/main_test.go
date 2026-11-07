package main

import "testing"

// RETAIN_DAYS keeps its meaning: unset is 730 days, and anything else is
// read as a whole number, what is not one as zero - so zero, below zero and
// a value typed wrong all keep everything.
func TestRetainDays(t *testing.T) {
	for _, c := range []struct {
		env  string
		want int
	}{
		{"", 730}, {"30", 30}, {"0", 0}, {"-1", -1}, {"abc", 0}, {"30.5", 0}, {" 30", 0},
	} {
		t.Setenv("RETAIN_DAYS", c.env)
		if got := retainDays(); got != c.want {
			t.Errorf("RETAIN_DAYS=%q: %d, want %d", c.env, got, c.want)
		}
	}
}
