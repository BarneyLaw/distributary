package protocol

import (
	"encoding/binary"
	"fmt"
)

// AckSize is the length of a ping/pong acknowledgement payload.
const AckSize = 8

// EncodeAck returns the ping payload carrying sentCount, the number of data
// messages sent on the connection so far: 8 bytes, big-endian. The client's
// WebSocket library echoes it in a pong once its reader reaches the ping, so
// sentCount - ackedCount counts messages still in flight.
func EncodeAck(sentCount uint64) []byte {
	return binary.BigEndian.AppendUint64(make([]byte, 0, AckSize), sentCount)
}

// DecodeAck reads the sent count back from a pong payload. Payloads that are
// not exactly AckSize bytes (such as a client's own keepalive pong) return
// ErrMalformed.
func DecodeAck(pongPayload []byte) (uint64, error) {
	if len(pongPayload) != AckSize {
		return 0, fmt.Errorf("%w: ack payload is %d bytes, want %d", ErrMalformed, len(pongPayload), AckSize)
	}
	return binary.BigEndian.Uint64(pongPayload), nil
}
