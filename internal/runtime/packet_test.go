package runtime

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestIPv4DecoderReassemblesSplitAndCoalescedPackets(t *testing.T) {
	first := testIPv4Packet(40, 1)
	second := testIPv4Packet(28, 2)
	decoder, err := NewIPv4Decoder(1024)
	if err != nil {
		t.Fatal(err)
	}
	var packets [][]byte
	emit := func(packet []byte) error {
		packets = append(packets, packet)
		return nil
	}
	if err := decoder.Feed(first[:13], emit); err != nil {
		t.Fatal(err)
	}
	combined := append(append([]byte(nil), first[13:]...), second...)
	if err := decoder.Feed(combined, emit); err != nil {
		t.Fatal(err)
	}
	if len(packets) != 2 || string(packets[0]) != string(first) || string(packets[1]) != string(second) {
		t.Fatalf("decoded packets = %d", len(packets))
	}
}

func TestIPv4DecoderRejectsInvalidAndUnboundedInput(t *testing.T) {
	decoder, err := NewIPv4Decoder(64)
	if err != nil {
		t.Fatal(err)
	}
	invalid := make([]byte, minimumIPv4Header)
	invalid[0] = 0x60
	if err := decoder.Feed(invalid, func([]byte) error { return nil }); !errors.Is(err, ErrInvalidIPv4Packet) {
		t.Fatalf("non-IPv4 error = %v", err)
	}
	decoder.Reset()
	oversized := testIPv4Packet(100, 0)
	if err := decoder.Feed(oversized[:minimumIPv4Header], func([]byte) error { return nil }); !errors.Is(err, ErrPendingLimit) {
		t.Fatalf("pending-limit error = %v", err)
	}
}

func testIPv4Packet(size int, marker byte) []byte {
	packet := make([]byte, size)
	packet[0] = 0x45
	binary.BigEndian.PutUint16(packet[2:4], uint16(size))
	for index := minimumIPv4Header; index < len(packet); index++ {
		packet[index] = marker
	}
	return packet
}
