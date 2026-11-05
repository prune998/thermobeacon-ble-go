package config

import (
	"testing"
)

// Benchmarks for the per-advertisement device filter. AcceptsDevice runs on
// the scan callback for every advertisement from every device in range, so
// the reject path must stay allocation-free: see the NormalizeMAC rewrite in
// internal/sensor (it used to construct a strings.NewReplacer per call).
// Run: go test ./internal/config -bench . -benchmem

func BenchmarkAcceptsDeviceUUIDFilterReject(b *testing.B) {
	c := &Config{Device: "10243784-1324-5413-b00c-9675981a8daa"}
	addr := "b6a2e5c8-9d1f-4e3a-8b7c-0a1b2c3d4e5f"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if c.AcceptsDevice(addr) {
			b.Fatal("unexpected accept")
		}
	}
}

func BenchmarkAcceptsDeviceMACFilterReject(b *testing.B) {
	c := &Config{Device: "db:5c:8d:a0:0f:21"}
	addr := "aa:bb:cc:dd:ee:ff"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if c.AcceptsDevice(addr) {
			b.Fatal("unexpected accept")
		}
	}
}
