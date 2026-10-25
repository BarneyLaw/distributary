// Package protocol defines the LiveRecall wire messages (assignment spec
// section 4). All messages are JSON text frames on one WebSocket, and every
// message carries a "type" field naming one of the constants below.
//
// This package knows nothing about connections or caption state: it only
// describes, decodes and encodes messages. JSON field names must match the
// spec exactly, because the instructor harness reads them.
package protocol

// Message type values carried in the "type" field.
const (
	TypeSourceHello   = "SOURCE_HELLO"
	TypeCaptionUpdate = "CAPTION_UPDATE"
	TypeSourceDone    = "SOURCE_DONE"
	TypeClientHello   = "CLIENT_HELLO"
	TypeHistory       = "HISTORY"
)

// SourceHello is the first message on the caption source's connection.
//
//	source -> server
type SourceHello struct {
	Type    string `json:"type"`
	Session string `json:"session"`
}

// CaptionUpdate is one revision of one caption segment.
//
//	source -> server, server -> client
//
// Segment starts at 1 and increases. Revision increases within a segment, with
// no fixed count. Final=true is durable: no older revision may replace it.
type CaptionUpdate struct {
	Type     string  `json:"type"`
	Session  string  `json:"session"`
	Segment  int64   `json:"segment"`
	Revision int64   `json:"revision"`
	SourceTS float64 `json:"source_ts"` // seconds since the Unix epoch, source's clock
	Final    bool    `json:"final"`
	Text     string  `json:"text"`
}

// SourceDone marks the end of a deterministic test run. It is test control
// only and is never forwarded to clients.
//
//	source -> server
type SourceDone struct {
	Type     string  `json:"type"`
	Session  string  `json:"session"`
	SourceTS float64 `json:"source_ts"`
	Segments int64   `json:"segments"` // number of finalized segments emitted
}

// ClientHello is the first message on a student connection.
//
//	client -> server
//
// LastSegment/LastRevision are the newest finalized state the client already
// holds (0, 0 for a new client), not the newest provisional it saw.
// HistorySeconds requests recent finalized context, at most 60.
type ClientHello struct {
	Type           string  `json:"type"`
	ClientID       int64   `json:"client_id"`
	Session        string  `json:"session"`
	LastSegment    int64   `json:"last_segment"`
	LastRevision   int64   `json:"last_revision"`
	HistorySeconds float64 `json:"history_seconds"`
}

// History is the server's reply to every ClientHello, possibly empty. Items
// are finals only, oldest to newest, with Segment > the hello's LastSegment.
//
//	server -> client
type History struct {
	Type  string          `json:"type"`
	Items []CaptionUpdate `json:"items"`
}
