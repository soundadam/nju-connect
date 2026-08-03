package runtime

import (
	"bytes"
	"io"
	"net/netip"
	"testing"
)

func BenchmarkIPv4DecoderFeed(b *testing.B) {
	packet := testIPv4Packet(1400, 1)
	input := make([]byte, 4*len(packet)+10)
	for offset := 0; offset < len(input)-10; offset += len(packet) {
		copy(input[offset:], packet)
	}
	copy(input[len(input)-10:], packet[:10])

	decoder, err := NewIPv4Decoder(16 * 1024)
	if err != nil {
		b.Fatal(err)
	}
	emit := func(packet []byte) error {
		clear(packet)
		return nil
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(input)))
	for b.Loop() {
		if err := decoder.Feed(input, emit); err != nil {
			b.Fatal(err)
		}
		decoder.Reset()
	}
}

func BenchmarkBuildICMPHeartbeat(b *testing.B) {
	token := testAgentToken()
	source := netip.MustParseAddr("10.0.0.2")
	destination := netip.MustParseAddr("10.0.0.1")
	b.ReportAllocs()
	b.SetBytes(76)
	for b.Loop() {
		packet, err := BuildICMPHeartbeat(source, destination, token)
		if err != nil {
			b.Fatal(err)
		}
		clear(packet)
	}
}

func BenchmarkSOCKSPayloadAccounting(b *testing.B) {
	payload := bytes.Repeat([]byte("payload"), 200)
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for b.Loop() {
		source := bytes.NewReader(payload)
		var destination bytes.Buffer
		writer := payloadAccountingWriter{Writer: &destination}
		if _, err := io.Copy(writer, source); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkNewUserspace(b *testing.B) {
	assigned := netip.MustParseAddr("10.20.30.40")
	b.ReportAllocs()
	for b.Loop() {
		userspace, err := NewUserspace(assigned)
		if err != nil {
			b.Fatal(err)
		}
		if err := userspace.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
