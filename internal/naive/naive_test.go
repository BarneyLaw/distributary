package naive

import (
	"encoding/json"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func startServer(t *testing.T) (*Server, string) {
	t.Helper()
	s := New(log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return s, "ws" + strings.TrimPrefix(ts.URL, "http")
}

func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetReadDeadline(time.Now().Add(5 * time.Second))
	return c
}

func send(t *testing.T, c *websocket.Conn, msg string) {
	t.Helper()
	if err := c.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func read(t *testing.T, c *websocket.Conn) []byte {
	t.Helper()
	_, b, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return b
}

func waitForClients(t *testing.T, s *Server, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.snapshot()) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected %d clients, got %d", want, len(s.snapshot()))
}

const clientHello = `{"type":"CLIENT_HELLO","client_id":1,"session":"L1","last_segment":0,"last_revision":0,"history_seconds":30}`

func TestClientGetsEmptyHistoryThenForwardedCaptions(t *testing.T) {
	s, url := startServer(t)

	client := dial(t, url)
	send(t, client, clientHello)
	var hist struct {
		Type  string            `json:"type"`
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(read(t, client), &hist); err != nil {
		t.Fatalf("HISTORY is not JSON: %v", err)
	}
	if hist.Type != "HISTORY" || hist.Items == nil || len(hist.Items) != 0 {
		t.Fatalf("want empty HISTORY with items:[], got %+v", hist)
	}
	waitForClients(t, s, 1)

	source := dial(t, url)
	send(t, source, `{"type":"SOURCE_HELLO","session":"L1"}`)
	caption := `{"type":"CAPTION_UPDATE","session":"L1","segment":1,"revision":1,"source_ts":1760000000.5,"final":false,"text":"TCP uses","phase":"extra"}`
	send(t, source, caption)
	send(t, source, `{"type":"SOURCE_DONE","session":"L1","segments":1}`)

	if got := string(read(t, client)); got != caption {
		t.Fatalf("caption not forwarded byte for byte:\n got %s\nwant %s", got, caption)
	}
}

func TestInvalidCaptionIsNotForwarded(t *testing.T) {
	s, url := startServer(t)
	client := dial(t, url)
	send(t, client, clientHello)
	read(t, client) // HISTORY
	waitForClients(t, s, 1)

	source := dial(t, url)
	send(t, source, `{"type":"SOURCE_HELLO","session":"L1"}`)
	send(t, source, `{"type":"CAPTION_UPDATE","segment":0,"revision":1,"source_ts":1,"final":true,"text":"bad segment"}`)
	good := `{"type":"CAPTION_UPDATE","segment":1,"revision":1,"source_ts":1,"final":true,"text":"ok"}`
	send(t, source, good)

	if got := string(read(t, client)); got != good {
		t.Fatalf("expected only the valid caption, got %s", got)
	}
}

func TestBadHelloClosesWithPolicyViolation(t *testing.T) {
	_, url := startServer(t)
	c := dial(t, url)
	send(t, c, `{"type":"HISTORY","items":[]}`)

	_, _, err := c.ReadMessage()
	var ce *websocket.CloseError
	if !asCloseError(err, &ce) || ce.Code != closePolicyViolation {
		t.Fatalf("want close code %d, got %v", closePolicyViolation, err)
	}
}

func TestDisconnectedClientIsRemoved(t *testing.T) {
	s, url := startServer(t)
	client := dial(t, url)
	send(t, client, clientHello)
	read(t, client)
	waitForClients(t, s, 1)

	client.Close()
	waitForClients(t, s, 0)
}

func asCloseError(err error, target **websocket.CloseError) bool {
	ce, ok := err.(*websocket.CloseError)
	if ok {
		*target = ce
	}
	return ok
}
