package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"time"
)

const (
	easyConnect767ServerFlightSize       = 122
	easyConnect767InitialFrameSize       = 76
	easyConnect767EstablishedFrameSize   = 68
	easyConnect767ReplyFrameSize         = 40
	easyConnect767InitialFrameHeader     = "\x17\x03\x01\x00\x3c\x00\x00\x00JJYY"
	easyConnect767EstablishedFrameHeader = "JJYY"
	easyConnect767ReplyFrameHeader       = "AABB"

	// These are canned vendor blobs read directly from svpnservice .rodata,
	// not a claim that the profile performs a standard TLS handshake.
	easyConnect767ClientPreface = "\x16\x03\x01\x00\x4d\x01\x00\x00\x49\x03\x01" +
		"\x46\x83\x74\x24\x01\x43\x16\x29\x1f\x95\x3e\xc9\x63\xb9\xfc\xb6" +
		"\x16\x00\xaa\x20\x15\xeb\x75\x07\x59\x40\x8b\x8d\x0c\xe1\x60\x43" +
		"\x20\xb1\x65\xf6\x7f\x5a\x6d\x2c\x54\xd2\x71\x69\x97\x72\x58\x65" +
		"\xc3\x40\x9a\x8a\x0f\xcd\x81\xd8\x2e\xdb\x7b\x8f\xf7\x1b\xc1\x80" +
		"\xc6\x00\x02\x00\x39\x01\x00"

	easyConnect767ClientFinal = "\x14\x03\x01\x00\x01\x01\x16\x03\x01\x00\x20" +
		"\x2b\xd1\xdf\x0e\xe2\x4c\xda\x7a\x36\x0e\xde\x24\x81\xb9\x79\x32" +
		"\x67\x8f\xf2\x88\x8c\xef\xa3\x7e\x50\x1c\x09\x6b\x09\x9c\x5a\x0d"
)

type EasyConnect767FixedPrefaceConfig struct {
	Dial CommandDialer
}

// EasyConnect767FixedPreface implements only the locked EasyConnect 7.6.7
// svpnservice wire profile. The TLS-shaped preface provides neither encryption
// nor certificate-based peer verification.
type EasyConnect767FixedPreface struct {
	config EasyConnect767FixedPrefaceConfig
	now    func() time.Time
}

var _ ProtocolProfile = (*EasyConnect767FixedPreface)(nil)

func NewEasyConnect767FixedPreface(config EasyConnect767FixedPrefaceConfig) (*EasyConnect767FixedPreface, error) {
	if config.Dial == nil {
		return nil, errors.New("raw gateway dialer is required")
	}
	return &EasyConnect767FixedPreface{config: config, now: time.Now}, nil
}

func (profile *EasyConnect767FixedPreface) ID() ProtocolProfileID {
	return ProfileEasyConnect767FixedPreface
}

func (profile *EasyConnect767FixedPreface) EvidenceID() ProtocolEvidenceID {
	return EvidenceEasyConnect767Binary
}

func (profile *EasyConnect767FixedPreface) SecurityProperties() ProtocolSecurityProperties {
	return ProtocolSecurityProperties{}
}

func (profile *EasyConnect767FixedPreface) EstablishedDataFraming() EstablishedDataFramingEvidence {
	return EstablishedDataFramingEvidence{
		Kind:     EstablishedDataFramingRawIPv4,
		Evidence: EvidenceLevelHypothesis,
	}
}

func (profile *EasyConnect767FixedPreface) Dial(ctx context.Context) (net.Conn, error) {
	raw, err := profile.config.Dial(ctx)
	if err != nil {
		if raw != nil {
			_ = raw.Close()
		}
		return nil, newStageFailure(StageUpstreamConnectFailed, nil)
	}
	if raw == nil {
		return nil, newStageFailure(StageUpstreamConnectFailed, nil)
	}
	keepOpen := false
	defer func() {
		if !keepOpen {
			_ = raw.Close()
		}
	}()
	stopMonitor := monitorContext(ctx, raw)
	defer stopMonitor()
	if err := raw.SetDeadline(protocolDeadline(ctx, profile.now())); err != nil {
		return nil, newStageFailure(StageProtocolPrefaceFailed, nil)
	}
	if err := writeFull(raw, []byte(easyConnect767ClientPreface)); err != nil {
		return nil, newStageFailure(StageProtocolPrefaceFailed, nil)
	}
	serverFlight := make([]byte, easyConnect767ServerFlightSize)
	defer clear(serverFlight)
	if _, err := io.ReadFull(raw, serverFlight); err != nil {
		return nil, newStageFailure(StageProtocolPrefaceFailed, nil)
	}
	if err := writeFull(raw, []byte(easyConnect767ClientFinal)); err != nil {
		return nil, newStageFailure(StageProtocolPrefaceFailed, nil)
	}
	if ctx.Err() != nil {
		return nil, newStageFailure(StageProtocolPrefaceFailed, nil)
	}
	if err := raw.SetDeadline(time.Time{}); err != nil {
		return nil, newStageFailure(StageProtocolPrefaceFailed, nil)
	}
	keepOpen = true
	return raw, nil
}

func (profile *EasyConnect767FixedPreface) WriteInitialCommandRequest(writer io.Writer, payload []byte) error {
	return writeEasyConnect767InitialFrame(writer, payload)
}

func (profile *EasyConnect767FixedPreface) ReadInitialCommandReply(reader io.Reader, payload []byte) error {
	return readEasyConnect767Reply(reader, payload)
}

func (profile *EasyConnect767FixedPreface) WriteEstablishedCommandRequest(writer io.Writer, payload []byte) error {
	if len(payload) != commandRequestSize {
		return errors.New("gateway request payload has an invalid length")
	}
	frame := make([]byte, easyConnect767EstablishedFrameSize)
	defer clear(frame)
	copy(frame, easyConnect767EstablishedFrameHeader)
	copy(frame[len(easyConnect767EstablishedFrameHeader):], payload)
	return writeFull(writer, frame)
}

func (profile *EasyConnect767FixedPreface) ReadEstablishedCommandReply(reader io.Reader, payload []byte) error {
	return readEasyConnect767Reply(reader, payload)
}

func (profile *EasyConnect767FixedPreface) WriteInitialDataRequest(writer io.Writer, payload []byte) error {
	return writeEasyConnect767InitialFrame(writer, payload)
}

func (profile *EasyConnect767FixedPreface) ReadInitialDataReply(reader io.Reader) (uint32, error) {
	payload := make([]byte, commandReplySize)
	defer clear(payload)
	if err := readEasyConnect767Reply(reader, payload); err != nil {
		return 0, err
	}
	return littleEndianReplyCode(payload), nil
}

func writeEasyConnect767InitialFrame(writer io.Writer, payload []byte) error {
	if len(payload) != commandRequestSize {
		return errors.New("gateway request payload has an invalid length")
	}
	frame := make([]byte, easyConnect767InitialFrameSize)
	defer clear(frame)
	copy(frame, easyConnect767InitialFrameHeader)
	copy(frame[len(easyConnect767InitialFrameHeader):], payload)
	return writeFull(writer, frame)
}

func readEasyConnect767Reply(reader io.Reader, payload []byte) error {
	if len(payload) != commandReplySize {
		return errors.New("gateway reply payload has an invalid length")
	}
	frame := make([]byte, easyConnect767ReplyFrameSize)
	defer clear(frame)
	if _, err := io.ReadFull(reader, frame); err != nil {
		return err
	}
	if !bytes.Equal(frame[:len(easyConnect767ReplyFrameHeader)], []byte(easyConnect767ReplyFrameHeader)) {
		return errors.New("gateway reply header is invalid")
	}
	copy(payload, frame[len(easyConnect767ReplyFrameHeader):])
	return nil
}
