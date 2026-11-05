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
		// comma-separated lists
		{"aa:bb:cc:dd:ee:01, aa:bb:cc:dd:ee:02", "AA:BB:CC:DD:EE:01", true},
		{"aa:bb:cc:dd:ee:01,aa:bb:cc:dd:ee:02", "aa:bb:cc:dd:ee:02", true},
		{"aa:bb:cc:dd:ee:01,aa:bb:cc:dd:ee:02", "aa:bb:cc:dd:ee:03", false},
		{"aa:bb:cc:dd:ee:01,,aa:bb:cc:dd:ee:02", "aa:bb:cc:dd:ee:02", true}, // empty middle entry
		{"aa:bb:cc:dd:ee:01,10243784-1324-5413-b00c-9675981a8daa", "10243784-1324-5413-B00C-9675981A8DAA", true},
		{"aa:bb:cc:dd:ee:01,db5c8da00f21", "DB:5C:8D:A0:0F:21", true}, // notation variants across entries
		{"db:5c:8d:a0:0f:21,aa:bb:cc:dd:ee:ff", "11:22:33:44:55:66", false},
		{"all, aa:bb:cc:dd:ee:01", "anything", true},     // "all" among entries wins
		{"*, aa:bb:cc:dd:ee:01", "anything", true},       // "*" among entries wins
		{" , ", "anything", true},                        // a list that trims to empty means all
		{"foobar1, foobar2", "aa:bb:cc:dd:ee:ff", false}, // garbage entries match only themselves
		{"foobar1, foobar2", "FOOBAR2", true},
	}
	for _, tc := range cases {
		cfg := &Config{Device: tc.device}
		if got := cfg.AcceptsDevice(tc.addr); got != tc.want {
			t.Errorf("device %q addr %q = %v, want %v", tc.device, tc.addr, got, tc.want)
		}
	}
}

func TestDescribeDevice(t *testing.T) {
	cases := []struct {
		device string
		want   string
	}{
		{"", "all ThermoBeacon, Govee and BTHome devices"},
		{"all", "all ThermoBeacon, Govee and BTHome devices"},
		{"*", "all ThermoBeacon, Govee and BTHome devices"},
		{" , ", "all ThermoBeacon, Govee and BTHome devices"},
		{"10243784-1324-5413-b00c-9675981a8daa", "device 10243784-1324-5413-b00c-9675981a8daa"},
		{"db:5c:8d:a0:0f:21", "device db:5c:8d:a0:0f:21"},
		{"aa:bb:cc:dd:ee:01, aa:bb:cc:dd:ee:02", "devices aa:bb:cc:dd:ee:01, aa:bb:cc:dd:ee:02"},
		{" aa:bb:cc:dd:ee:01 ,10243784-1324-5413-b00c-9675981a8daa ", "devices aa:bb:cc:dd:ee:01, 10243784-1324-5413-b00c-9675981a8daa"},
	}
	for _, tc := range cases {
		cfg := &Config{Device: tc.device}
		if got := cfg.DescribeDevice(); got != tc.want {
			t.Errorf("DescribeDevice(%q) = %q, want %q", tc.device, got, tc.want)
		}
	}
}
