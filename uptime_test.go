package main

import "testing"

func TestParseUptimeSeconds(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		want   int64
		wantOK bool
	}{
		{"full", "3day(s) 1h 46m 13s", 3*86400 + 1*3600 + 46*60 + 13, true},
		{"zero days", "0day(s) 5m 3s", 5*60 + 3, true},
		{"hours only", "10h", 10 * 3600, true},
		{"seconds only", "45s", 45, true},
		{"leading zero seconds", "3day(s) 0h 12m 03s", 3*86400 + 12*60 + 3, true},
		{"empty", "", 0, false},
		{"whitespace only", "   ", 0, false},
		{"garbage", "not-an-uptime", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseUptimeSeconds(tc.in)
			if ok != tc.wantOK {
				t.Fatalf("parseUptimeSeconds(%q) ok = %v, want %v", tc.in, ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Fatalf("parseUptimeSeconds(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}
