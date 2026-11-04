package config

import (
	"testing"
)

func TestAcceptsDevice(t *testing.T) {
	cases := []struct {
		device string
		addr   string
		want   bool
	}{
		{"10243784-1324-5413-b00c-9675981a8daa", "10243784-1324-5413-b00c-9675981a8daa", true},
		{"10243784-1324-5413-b00c-9675981a8daa", "10243784-1324-5413-B00C-9675981A8DAA", true},
		{"10243784-1324-5413-b00c-9675981a8daa", "00000000-0000-0000-0000-000000000000", false},
		{"db:5c:8d:a0:0f:21", "DB:5C:8D:A0:0F:21", true},
		{"DB5C8DA00F21", "db:5c:8d:a0:0f:21", true},
		{"all", "anything", true},
		{"", "anything", true},
		{"*", "anything", true},
		{"db:5c:8d:a0:0f:21", "aa:bb:cc:dd:ee:ff", false},
	}
	for _, tc := range cases {
		cfg := &Config{Device: tc.device}
		if got := cfg.AcceptsDevice(tc.addr); got != tc.want {
			t.Errorf("device %q addr %q = %v, want %v", tc.device, tc.addr, got, tc.want)
		}
	}
}
