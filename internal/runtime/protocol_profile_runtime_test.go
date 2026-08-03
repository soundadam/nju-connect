package runtime

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
)

func TestParseProtocolProfileIDAcceptsOnlyNamedProfiles(t *testing.T) {
	for _, want := range []ProtocolProfileID{ProfileCommunityUTLSCompat, ProfileEasyConnect767FixedPreface} {
		got, err := ParseProtocolProfileID(string(want))
		if err != nil || got != want {
			t.Fatalf("parse %q = %q, %v", want, got, err)
		}
	}
	const sensitive = "secret-profile-value"
	if _, err := ParseProtocolProfileID(sensitive); err == nil || strings.Contains(err.Error(), sensitive) {
		t.Fatalf("unknown profile error = %v", err)
	}
}

type rawTestProtocolProfile struct {
	dial CommandDialer
}

func newRawTestProtocolProfile(dial CommandDialer) ProtocolProfile {
	return &rawTestProtocolProfile{dial: dial}
}

func (*rawTestProtocolProfile) ID() ProtocolProfileID { return ProfileCommunityUTLSCompat }
func (*rawTestProtocolProfile) EvidenceID() ProtocolEvidenceID {
	return EvidenceReverseTestLiveCompat
}
func (*rawTestProtocolProfile) SecurityProperties() ProtocolSecurityProperties {
	return ProtocolSecurityProperties{Encrypted: true, PeerVerified: true}
}
func (profile *rawTestProtocolProfile) Dial(ctx context.Context) (net.Conn, error) {
	return profile.dial(ctx)
}
func (*rawTestProtocolProfile) WriteInitialCommandRequest(writer io.Writer, payload []byte) error {
	return writeFull(writer, payload)
}
func (*rawTestProtocolProfile) ReadInitialCommandReply(reader io.Reader, payload []byte) error {
	_, err := io.ReadFull(reader, payload)
	return err
}
func (*rawTestProtocolProfile) WriteEstablishedCommandRequest(writer io.Writer, payload []byte) error {
	return writeFull(writer, payload)
}
func (*rawTestProtocolProfile) ReadEstablishedCommandReply(reader io.Reader, payload []byte) error {
	_, err := io.ReadFull(reader, payload)
	return err
}
func (*rawTestProtocolProfile) WriteInitialDataRequest(writer io.Writer, payload []byte) error {
	return writeFull(writer, payload)
}
func (*rawTestProtocolProfile) ReadInitialDataReply(reader io.Reader) (uint32, error) {
	var reply [1]byte
	_, err := io.ReadFull(reader, reply[:])
	return uint32(reply[0]), err
}
func (*rawTestProtocolProfile) EstablishedDataFraming() EstablishedDataFramingEvidence {
	return EstablishedDataFramingEvidence{Kind: EstablishedDataFramingRawIPv4, Evidence: EvidenceLevelLiveObservation}
}
