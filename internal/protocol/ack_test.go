package protocol

import (
	"errors"
	"math"
	"testing"
)

func TestAckRoundTrip(t *testing.T) {
	for _, sentCount := range []uint64{0, 1, 255, 1 << 32, math.MaxUint64} {
		pongPayload := EncodeAck(sentCount)
		if len(pongPayload) != AckSize {
			t.Fatalf("EncodeAck(%d) is %d bytes, want %d", sentCount, len(pongPayload), AckSize)
		}
		got, err := DecodeAck(pongPayload)
		if err != nil || got != sentCount {
			t.Errorf("DecodeAck(EncodeAck(%d)) = %d, %v", sentCount, got, err)
		}
	}
}

func TestAckBigEndian(t *testing.T) {
	want := []byte{0, 0, 0, 0, 0, 0, 1, 2}
	if got := EncodeAck(258); string(got) != string(want) {
		t.Errorf("EncodeAck(258) = %v, want %v", got, want)
	}
}

func TestDecodeAckRejectsWrongLength(t *testing.T) {
	for _, pongPayload := range [][]byte{nil, {}, {1, 2, 3}, make([]byte, 9), []byte("keepalive")} {
		if _, err := DecodeAck(pongPayload); !errors.Is(err, ErrMalformed) {
			t.Errorf("DecodeAck(%q) err = %v, want ErrMalformed", pongPayload, err)
		}
	}
}
