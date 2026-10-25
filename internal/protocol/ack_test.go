package protocol

import (
	"errors"
	"math"
	"testing"
)

func TestAckRoundTrip(t *testing.T) {
	for _, seq := range []uint64{0, 1, 255, 1 << 32, math.MaxUint64} {
		payload := EncodeAck(seq)
		if len(payload) != AckSize {
			t.Fatalf("EncodeAck(%d) is %d bytes, want %d", seq, len(payload), AckSize)
		}
		got, err := DecodeAck(payload)
		if err != nil || got != seq {
			t.Errorf("DecodeAck(EncodeAck(%d)) = %d, %v", seq, got, err)
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
	for _, payload := range [][]byte{nil, {}, {1, 2, 3}, make([]byte, 9), []byte("keepalive")} {
		if _, err := DecodeAck(payload); !errors.Is(err, ErrMalformed) {
			t.Errorf("DecodeAck(%q) err = %v, want ErrMalformed", payload, err)
		}
	}
}
