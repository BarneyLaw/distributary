// Package itest runs the whole server in-process (router, source handler,
// session and fanout behind httptest) and drives it with raw WebSocket
// clients. Unit tests cover each package alone; these cover the seams.
package itest

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/BarneyLaw/distributary/internal/fanout"
	"github.com/BarneyLaw/distributary/internal/server"
	"github.com/BarneyLaw/distributary/internal/session"
	"github.com/BarneyLaw/distributary/internal/source"
)

// A burst of finals arrives, then the source goes quiet. Every connected
// client must still get every final.
func TestBurstOfFinalsThenSilenceReachesClient(t *testing.T) {
	sess := session.New(session.DefaultConfig())
	quiet := log.New(io.Discard, "", 0)
	ts := httptest.NewServer(&server.Router{Sources: source.New(sess, quiet), Clients: fanout.NewHub(sess), Log: quiet})
	defer ts.Close()
	url := "ws" + strings.TrimPrefix(ts.URL, "http")

	cl, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	cl.WriteMessage(websocket.TextMessage, []byte(`{"type":"CLIENT_HELLO","client_id":1,"session":"L1","last_segment":0,"last_revision":0,"history_seconds":30}`))
	cl.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := cl.ReadMessage(); err != nil { // HISTORY
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond) // writer is now waiting on wake

	src, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	src.WriteMessage(websocket.TextMessage, []byte(`{"type":"SOURCE_HELLO","session":"L1"}`))
	for seg := 1; seg <= 3; seg++ {
		src.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf(
			`{"type":"CAPTION_UPDATE","session":"L1","segment":%d,"revision":1,"source_ts":1760000000.5,"final":true,"text":"t"}`, seg)))
	}

	var got []int
	cl.SetReadDeadline(time.Now().Add(2 * time.Second))
	for len(got) < 3 {
		_, raw, err := cl.ReadMessage()
		if err != nil {
			break
		}
		var m struct{ Segment int }
		json.Unmarshal(raw, &m)
		got = append(got, m.Segment)
	}
	if len(got) != 3 {
		t.Fatalf("client received finals %v within 2 s, want [1 2 3]: the rest stay pending until the next update", got)
	}
}
