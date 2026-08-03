package runtime

import (
	"encoding/binary"
	"errors"
)

const (
	minimumIPv4Header = 20
	maximumIPv4Packet = 65535
	defaultMaxPending = maximumIPv4Packet * 2
)

var ErrInvalidIPv4Packet = errors.New("invalid IPv4 packet")
var ErrPendingLimit = errors.New("IPv4 stream pending limit exceeded")

func validateIPv4Packet(packet []byte) (int, error) {
	if len(packet) < minimumIPv4Header || packet[0]>>4 != 4 {
		return 0, ErrInvalidIPv4Packet
	}
	headerLength := int(packet[0]&0x0f) * 4
	if headerLength < minimumIPv4Header || headerLength > len(packet) {
		return 0, ErrInvalidIPv4Packet
	}
	totalLength := int(binary.BigEndian.Uint16(packet[2:4]))
	if totalLength < headerLength || totalLength > maximumIPv4Packet {
		return 0, ErrInvalidIPv4Packet
	}
	return totalLength, nil
}

type IPv4Decoder struct {
	pending    []byte
	maxPending int
}

func NewIPv4Decoder(maxPending int) (*IPv4Decoder, error) {
	if maxPending == 0 {
		maxPending = defaultMaxPending
	}
	if maxPending < minimumIPv4Header {
		return nil, ErrPendingLimit
	}
	return &IPv4Decoder{maxPending: maxPending}, nil
}

// Feed accepts arbitrary TLS record boundaries and emits complete IPv4
// packets. Emitted packet slices are independent from the decoder buffer.
func (decoder *IPv4Decoder) Feed(data []byte, emit func([]byte) error) error {
	if len(decoder.pending)+len(data) > decoder.maxPending {
		return ErrPendingLimit
	}
	decoder.pending = append(decoder.pending, data...)
	for len(decoder.pending) >= minimumIPv4Header {
		totalLength, err := validateIPv4Packet(decoder.pending)
		if err != nil {
			return err
		}
		if totalLength > decoder.maxPending {
			return ErrPendingLimit
		}
		if len(decoder.pending) < totalLength {
			return nil
		}
		packet := append([]byte(nil), decoder.pending[:totalLength]...)
		if err := emit(packet); err != nil {
			clear(packet)
			return err
		}
		decoder.pending = decoder.pending[totalLength:]
	}
	if len(decoder.pending) == 0 {
		decoder.pending = nil
	}
	// The pending slice is always backed by decoder-owned storage because Feed
	// appends into it. Keep the bounded backing array for a short IPv4 header
	// tail instead of allocating and copying that tail on every read boundary.
	return nil
}

func (decoder *IPv4Decoder) Reset() {
	clear(decoder.pending)
	decoder.pending = nil
}
