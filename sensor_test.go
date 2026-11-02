package main

import "testing"

func TestNormalizeMAC(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"DB:5C:8D:A0:0F:21", "db:5c:8d:a0:0f:21", false},
		{"db5c8da00f21", "db:5c:8d:a0:0f:21", false},
		{"db-5c-8d-a0-0f-21", "db:5c:8d:a0:0f:21", false},
		{"aa:bb:cc:dd:ee", "", true},
		{"aa:bb:cc:dd:ee:ff:00", "", true},
		{"zz:bb:cc:dd:ee:ff", "", true},
		{"10243784-1324-5413-b00c-9675981a8daa", "", true}, // a UUID, not a MAC
	}
	for _, tc := range cases {
		got, err := normalizeMAC(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("normalizeMAC(%q) = %q, want error", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeMAC(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("normalizeMAC(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsMACAddress(t *testing.T) {
	if !isMACAddress("db:5c:8d:a0:0f:21") {
		t.Error("MAC not recognized")
	}
	if isMACAddress("10243784-1324-5413-b00c-9675981a8daa") {
		t.Error("UUID recognized as MAC")
	}
}

func TestMACByteConversions(t *testing.T) {
	mac := "db:5c:8d:a0:0f:21"

	be, err := macToBytes(mac)
	if err != nil {
		t.Fatalf("macToBytes: %v", err)
	}
	wantBE := []byte{0xdb, 0x5c, 0x8d, 0xa0, 0x0f, 0x21}
	for i, w := range wantBE {
		if be[i] != w {
			t.Errorf("macToBytes[%d] = %#02x, want %#02x", i, be[i], w)
		}
	}

	bd, err := macToBDAddr(mac)
	if err != nil {
		t.Fatalf("macToBDAddr: %v", err)
	}
	wantBD := []byte{0x21, 0x0f, 0xa0, 0x8d, 0x5c, 0xdb}
	for i, w := range wantBD {
		if bd[i] != w {
			t.Errorf("macToBDAddr[%d] = %#02x, want %#02x", i, bd[i], w)
		}
	}

	if got := macFromLSBFirst(wantBD); got != mac {
		t.Errorf("macFromLSBFirst = %q, want %q", got, mac)
	}
	if got := macFromMSBFirst(wantBE); got != mac {
		t.Errorf("macFromMSBFirst = %q, want %q", got, mac)
	}
}

func TestReadingString(t *testing.T) {
	r := &Reading{MAC: "db:5c:8d:a0:0f:21", Temperature: 19.25, Humidity: 58, VoltageMV: 2880, Uptime: 2131727, BatteryPct: -1}
	want := "db:5c:8d:a0:0f:21: 19.25 degC, 58.00 %, 2880 mV, uptime 2131727 s"
	if got := r.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	r.BatteryPct = 88
	want += ", battery 88 %"
	if got := r.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
