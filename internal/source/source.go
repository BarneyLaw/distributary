package source

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"

	"github.com/BarneyLaw/distributary/internal/protocol"
	"github.com/BarneyLaw/distributary/internal/session"
	"github.com/gorilla/websocket"
)

type Handler struct {
	session 	*session.Session
	log     	*log.Logger

	mu 			sync.Mutex
	socket  	*websocket.Conn // currently active source connection, or nil if none

	verdicts	[6]atomic.Uint64
	malformed 	atomic.Uint64
	done 		atomic.Uint64
}

func New(s *session.Session, log *log.Logger) *Handler {
	return &Handler{
		session: s,
		log:     log,
	}
}

// Serve runs one source connection until it closes. The router has already
// upgraded it, decoded SOURCE_HELLO and cleared the read deadline.
//
// Decisions, and why:
//
//   - A bad frame is counted and skipped, never a reason to disconnect: the
//     source is our only producer, and dropping it loses every caption after.
//   - Nothing is ever sent to the source. caption_source.py never reads, so
//     anything we sent would pile up. Pongs to its keepalive pings are sent
//     by gorilla automatically while this loop is reading.
//   - No read deadline: a source may legitimately go quiet (a pause, a bursty
//     workload). A source that really disconnects makes ReadMessage fail.
//   - SOURCE_DONE is test control: logged, not forwarded, connection kept.
//   - One source at a time, newest wins (see takeOver).
func (h *Handler) Handle(ws *websocket.Conn, hello *protocol.SourceHello) {
	h.takeOver(ws)
	defer h.release(ws)
	h.log.Printf("source connected: session=%s", hello.Session)
	defer func() { h.log.Printf("source disconnected: %s", h.Stats()) }()

	for {
		_, raw, err := ws.ReadMessage()
		if err != nil {
			return
		}

		msg, err := protocol.DecodeSourceMessage(raw)
		if err != nil {
			h.malformed.Add(1)
			continue // a single malformed message should not end the session
		}

		switch m := msg.(type) {
		case *protocol.CaptionUpdate:
			h.verdicts[h.session.Apply(m)].Add(1)
		
		case *protocol.SourceDone:
			h.done.Add(1)
			h.log.Printf("source done: %d segments; %s", m.Segments, h.Stats())
		}
	}
}

// takeOver makes ws the active source. If another source is still
// connected, for example a crashed source whose restart arrived before we
// noticed the old connection was dead, the old connection is closed: its
// Serve loop then ends on its next read. For a moment both loops may be
// inside Apply, which is safe because Apply takes the session's write lock.
func (h *Handler) takeOver(ws *websocket.Conn) {
	// this lock is necessary to avoid a race between two sources connecting at the same time.
	h.mu.Lock()
	prev := h.socket
	h.socket = ws
	h.mu.Unlock()

	if prev != nil {
		h.log.Printf("source takeover: closing previous connection")
		prev.Close()
	}
}

// release forgets ws, unless a newer source has already taken over.
func (h *Handler) release(ws *websocket.Conn) {
	h.mu.Lock()
	if h.socket == ws {
		h.socket = nil
	}
	h.mu.Unlock()
}

// Stats counts what the source has sent since the server started. It is
// safe to call from any goroutine, for example a periodic stats logger.
type Stats struct {
	// One counter per session.Verdict.
	Accepted     uint64
	Duplicate    uint64
	Stale        uint64
	AlreadyFinal uint64
	TooOld       uint64
 
	Malformed uint64 // frames that failed protocol decoding
	Done      uint64 // SOURCE_DONE messages received
}
 
// Stats returns the current counters.
func (h *Handler) Stats() Stats {
	return Stats{
		Accepted:     h.verdicts[session.Accepted].Load(),
		Duplicate:    h.verdicts[session.Duplicate].Load(),
		Stale:        h.verdicts[session.Stale].Load(),
		AlreadyFinal: h.verdicts[session.AlreadyFinal].Load(),
		TooOld:       h.verdicts[session.TooOld].Load(),
		Malformed:    h.malformed.Load(),
		Done:         h.done.Load(),
	}
}
 
func (s Stats) String() string {
	return fmt.Sprintf("accepted=%d duplicate=%d stale=%d already_final=%d too_old=%d malformed=%d done=%d",
		s.Accepted, s.Duplicate, s.Stale, s.AlreadyFinal, s.TooOld, s.Malformed, s.Done)
}




