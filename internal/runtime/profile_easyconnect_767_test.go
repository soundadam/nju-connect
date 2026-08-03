package runtime

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

const (
	easyConnect767ClientPrefaceFixtureHex = "160301004d01000049030146837424014316291f953ec963b9fcb61600aa2015" +
		"eb750759408b8d0ce1604320b165f67f5a6d2c54d2716997725865c3409a8a0f" +
		"cd81d82edb7b8ff71bc180c6000200390100"
	easyConnect767ClientFinalFixtureHex = "14030100010116030100202bd1df0ee24cda7a360ede2481b97932678ff2888c" +
		"efa37e501c096b099c5a0d"
)

func TestEasyConnect767FixedPrefaceExactWireAndOpaqueServerFlight(t *testing.T) {
	if len(easyConnect767ClientPreface) != 82 || easyConnect767ServerFlightSize != 122 || len(easyConnect767ClientFinal) != 43 {
		t.Fatalf("preface sizes = %d/%d/%d", len(easyConnect767ClientPreface), easyConnect767ServerFlightSize, len(easyConnect767ClientFinal))
	}
	client, server := net.Pipe()
	recorded := &recordingConn{Conn: client}
	wire := make(chan struct {
		preface []byte
		final   []byte
		err     error
	}, 1)
	release := make(chan struct{})
	defer close(release)
	go func() {
		defer server.Close()
		preface := make([]byte, len(easyConnect767ClientPreface))
		if _, err := io.ReadFull(server, preface); err != nil {
			wire <- struct {
				preface []byte
				final   []byte
				err     error
			}{err: err}
			return
		}
		opaque := bytes.Repeat([]byte{0xa5}, easyConnect767ServerFlightSize)
		if _, err := server.Write(opaque[:17]); err == nil {
			_, err = server.Write(opaque[17:])
			if err != nil {
				wire <- struct {
					preface []byte
					final   []byte
					err     error
				}{err: err}
				return
			}
		} else {
			wire <- struct {
				preface []byte
				final   []byte
				err     error
			}{err: err}
			return
		}
		final := make([]byte, len(easyConnect767ClientFinal))
		_, err := io.ReadFull(server, final)
		wire <- struct {
			preface []byte
			final   []byte
			err     error
		}{preface: preface, final: final, err: err}
		<-release
	}()
	profile, err := NewEasyConnect767FixedPreface(EasyConnect767FixedPrefaceConfig{
		Dial: func(context.Context) (net.Conn, error) { return recorded, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	fixedNow := time.Now()
	profile.now = func() time.Time { return fixedNow }
	connection, err := profile.Dial(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result := <-wire
	if result.err != nil {
		t.Fatal(result.err)
	}
	wantPreface := decodeProfileFixture(t, easyConnect767ClientPrefaceFixtureHex)
	wantFinal := decodeProfileFixture(t, easyConnect767ClientFinalFixtureHex)
	if !bytes.Equal(result.preface, wantPreface) || !bytes.Equal(result.final, wantFinal) {
		t.Fatalf("fixed profile wire = %x / %x", result.preface, result.final)
	}
	deadlines, _ := recorded.snapshot()
	if len(deadlines) != 2 || !deadlines[0].Equal(fixedNow.Add(gatewayProtocolTimeout)) || !deadlines[1].IsZero() {
		t.Fatalf("preface deadlines = %v", deadlines)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEasyConnect767FixedPrefaceRequiresCompleteServerFlight(t *testing.T) {
	client, server := net.Pipe()
	recorded := &recordingConn{Conn: client}
	go func() {
		defer server.Close()
		_, _ = io.CopyN(io.Discard, server, int64(len(easyConnect767ClientPreface)))
		_, _ = server.Write(make([]byte, easyConnect767ServerFlightSize-1))
	}()
	profile, err := NewEasyConnect767FixedPreface(EasyConnect767FixedPrefaceConfig{
		Dial: func(context.Context) (net.Conn, error) { return recorded, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := profile.Dial(context.Background())
	if connection != nil || err == nil {
		t.Fatalf("connection=%v error=%v", connection, err)
	}
	if stage, ok := failureStageOf(err); !ok || stage != StageProtocolPrefaceFailed {
		t.Fatalf("preface stage = %q, %t", stage, ok)
	}
	if strings.Contains(err.Error(), "server") {
		t.Fatalf("preface error exposed details: %v", err)
	}
	_, closes := recorded.snapshot()
	if closes == 0 {
		t.Fatal("short server flight did not close connection")
	}
}

func TestEasyConnect767FixedPrefaceCancellationClosesPendingFlight(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	recorded := &recordingConn{Conn: client}
	prefaceRead := make(chan struct{})
	go func() {
		_, _ = io.CopyN(io.Discard, server, int64(len(easyConnect767ClientPreface)))
		close(prefaceRead)
		_, _ = io.Copy(io.Discard, server)
	}()
	profile, err := NewEasyConnect767FixedPreface(EasyConnect767FixedPrefaceConfig{
		Dial: func(context.Context) (net.Conn, error) { return recorded, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := profile.Dial(ctx)
		result <- err
	}()
	<-prefaceRead
	cancel()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("canceled fixed preface succeeded")
		}
		if stage, ok := failureStageOf(err); !ok || stage != StageProtocolPrefaceFailed {
			t.Fatalf("canceled preface stage = %q, %t", stage, ok)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled fixed preface did not return")
	}
	_, closes := recorded.snapshot()
	if closes == 0 {
		t.Fatal("canceled fixed preface did not close raw connection")
	}
}

func TestEasyConnect767ProfileMetadata(t *testing.T) {
	profile, err := NewEasyConnect767FixedPreface(EasyConnect767FixedPrefaceConfig{
		Dial: func(context.Context) (net.Conn, error) { return nil, errors.New("unused") },
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.ID() != ProfileEasyConnect767FixedPreface || profile.EvidenceID() != EvidenceEasyConnect767Binary {
		t.Fatalf("profile identity = %q/%q", profile.ID(), profile.EvidenceID())
	}
	if got := profile.SecurityProperties(); got.Encrypted || got.PeerVerified {
		t.Fatalf("security properties = %+v", got)
	}
	if got := profile.EstablishedDataFraming(); got.Kind != EstablishedDataFramingRawIPv4 || got.Evidence != EvidenceLevelHypothesis {
		t.Fatalf("data framing = %+v", got)
	}
}

func TestEasyConnect767InitialAndEstablishedFramesAreDistinct(t *testing.T) {
	profile, err := NewEasyConnect767FixedPreface(EasyConnect767FixedPrefaceConfig{
		Dial: func(context.Context) (net.Conn, error) { return nil, errors.New("unused") },
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, commandRequestSize)
	for index := range payload {
		payload[index] = byte(index)
	}
	var initial bytes.Buffer
	if err := profile.WriteInitialCommandRequest(&initial, payload); err != nil {
		t.Fatal(err)
	}
	wantInitial := append([]byte(easyConnect767InitialFrameHeader), payload...)
	if initial.Len() != easyConnect767InitialFrameSize || !bytes.Equal(initial.Bytes(), wantInitial) {
		t.Fatalf("initial command frame = %x", initial.Bytes())
	}
	var data bytes.Buffer
	if err := profile.WriteInitialDataRequest(&data, payload); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data.Bytes(), wantInitial) {
		t.Fatalf("initial data frame = %x", data.Bytes())
	}
	heartbeatPayload := append([]byte(nil), payload...)
	binary.LittleEndian.PutUint32(heartbeatPayload[:4], 3)
	var established bytes.Buffer
	if err := profile.WriteEstablishedCommandRequest(&established, heartbeatPayload); err != nil {
		t.Fatal(err)
	}
	wantEstablished := append([]byte(easyConnect767EstablishedFrameHeader), heartbeatPayload...)
	if established.Len() != easyConnect767EstablishedFrameSize || !bytes.Equal(established.Bytes(), wantEstablished) {
		t.Fatalf("established command frame = %x", established.Bytes())
	}
	if binary.LittleEndian.Uint32(established.Bytes()[4:8]) != 3 {
		t.Fatalf("established command op = %x", established.Bytes()[4:8])
	}
}

func TestEasyConnect767ReplyFramingHandlesFragmentationCoalescingAndMarkerFailures(t *testing.T) {
	profile, err := NewEasyConnect767FixedPreface(EasyConnect767FixedPrefaceConfig{
		Dial: func(context.Context) (net.Conn, error) { return nil, errors.New("unused") },
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, commandReplySize)
	for index := range payload {
		payload[index] = byte(0x80 + index)
	}
	frame := append([]byte(easyConnect767ReplyFrameHeader), payload...)
	got := make([]byte, commandReplySize)
	if err := profile.ReadInitialCommandReply(iotest.OneByteReader(bytes.NewReader(frame)), got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("fragmented reply = %x", got)
	}
	coalesced := bytes.NewBuffer(append(append([]byte(nil), frame...), 0xcc, 0xdd))
	if err := profile.ReadEstablishedCommandReply(coalesced, got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(coalesced.Bytes(), []byte{0xcc, 0xdd}) {
		t.Fatalf("coalesced tail = %x", coalesced.Bytes())
	}
	wrongMarker := append([]byte("BAAD"), payload...)
	if err := profile.ReadInitialCommandReply(bytes.NewReader(wrongMarker), got); err == nil {
		t.Fatal("wrong reply marker was accepted")
	}
	if err := profile.ReadEstablishedCommandReply(bytes.NewReader(wrongMarker), got); err == nil {
		t.Fatal("wrong established reply marker was accepted")
	}
	if err := profile.ReadInitialCommandReply(bytes.NewReader(frame[:len(frame)-1]), got); err == nil {
		t.Fatal("short reply was accepted")
	}
}

func TestEasyConnect767InitialDataReplyReadsFullUint32Code(t *testing.T) {
	profile, err := NewEasyConnect767FixedPreface(EasyConnect767FixedPrefaceConfig{
		Dial: func(context.Context) (net.Conn, error) { return nil, errors.New("unused") },
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, commandReplySize)
	binary.LittleEndian.PutUint32(payload[:4], 0x00000101)
	frame := append([]byte(easyConnect767ReplyFrameHeader), payload...)
	code, err := profile.ReadInitialDataReply(bytes.NewReader(frame))
	if err != nil {
		t.Fatal(err)
	}
	if code != 0x00000101 {
		t.Fatalf("data reply code = %#x", code)
	}
}

func decodeProfileFixture(t *testing.T, encoded string) []byte {
	t.Helper()
	fixture, err := hex.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}
