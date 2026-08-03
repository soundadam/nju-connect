package runtime

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
)

type ProtocolProfileID string

const (
	ProfileEasyConnect767FixedPreface ProtocolProfileID = "easyconnect-7.6.7"
	ProfileCommunityUTLSCompat        ProtocolProfileID = "community-utls"
)

type ProtocolEvidenceID string

const (
	EvidenceEasyConnect767Binary  ProtocolEvidenceID = "easyconnect_7_6_7_svpnservice_77b5d41aa722997d7a062775251fa1ca4f095fb133ac1c3583f0353705ad9f1b"
	EvidenceReverseTestLiveCompat ProtocolEvidenceID = "reverse_test_live_2026_07_18"
)

type ProtocolSecurityProperties struct {
	Encrypted    bool
	PeerVerified bool
}

type ProtocolProfileMetadata struct {
	ID       ProtocolProfileID
	Evidence ProtocolEvidenceID
	Security ProtocolSecurityProperties
}

type EvidenceLevel string

const (
	EvidenceLevelBinaryProven         EvidenceLevel = "binary_proven"
	EvidenceLevelPublicImplementation EvidenceLevel = "public_implementation"
	EvidenceLevelLiveObservation      EvidenceLevel = "live_observation"
	EvidenceLevelHypothesis           EvidenceLevel = "hypothesis"
)

type EstablishedDataFramingKind string

const EstablishedDataFramingRawIPv4 EstablishedDataFramingKind = "raw_ipv4"

type EstablishedDataFramingEvidence struct {
	Kind     EstablishedDataFramingKind
	Evidence EvidenceLevel
}

// ProtocolProfile owns only the version-sensitive gateway wire boundary.
// Session lifecycle, readiness, userspace networking, SOCKS, and traffic
// accounting remain independent of the selected profile.
type ProtocolProfile interface {
	ID() ProtocolProfileID
	EvidenceID() ProtocolEvidenceID
	SecurityProperties() ProtocolSecurityProperties
	Dial(context.Context) (net.Conn, error)
	WriteInitialCommandRequest(io.Writer, []byte) error
	ReadInitialCommandReply(io.Reader, []byte) error
	WriteEstablishedCommandRequest(io.Writer, []byte) error
	ReadEstablishedCommandReply(io.Reader, []byte) error
	WriteInitialDataRequest(io.Writer, []byte) error
	ReadInitialDataReply(io.Reader) (uint32, error)
	EstablishedDataFraming() EstablishedDataFramingEvidence
}

func ParseProtocolProfileID(value string) (ProtocolProfileID, error) {
	switch ProtocolProfileID(value) {
	case ProfileCommunityUTLSCompat:
		return ProfileCommunityUTLSCompat, nil
	case ProfileEasyConnect767FixedPreface:
		return ProfileEasyConnect767FixedPreface, nil
	default:
		return "", errors.New("unsupported native profile")
	}
}

func ProtocolProfileInfo(profile ProtocolProfile) ProtocolProfileMetadata {
	if profile == nil {
		return ProtocolProfileMetadata{}
	}
	return ProtocolProfileMetadata{
		ID:       profile.ID(),
		Evidence: profile.EvidenceID(),
		Security: profile.SecurityProperties(),
	}
}

func littleEndianReplyCode(payload []byte) uint32 {
	if len(payload) < 4 {
		return 0
	}
	return binary.LittleEndian.Uint32(payload[:4])
}
