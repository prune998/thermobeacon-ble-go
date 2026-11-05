package sensor

import "testing"

// Benchmarks for the helpers on the per-advertisement hot path (device
// filter, ThermoBeacon decode). Run: go test ./internal/sensor -bench . -benchmem

func BenchmarkNormalizeMAC(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := NormalizeMAC("aa:bb:cc:dd:ee:ff"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNormalizeMACInvalidUUID(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := NormalizeMAC("b6a2e5c8-9d1f-4e3a-8b7c-0a1b2c3d4e5f"); err == nil {
			b.Fatal("expected error")
		}
	}
}

func BenchmarkMACFromMSBFirst(b *testing.B) {
	buf := []byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = MACFromMSBFirst(buf)
	}
}
