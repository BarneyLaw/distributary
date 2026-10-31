package fanout

import (
	"sync"

	"github.com/BarneyLaw/distributary/internal/protocol"
)

// Session here are just dummy class. Will be replaced with the real session later.
type Session struct {
	caption      *protocol.SourceHello
	notification chan struct{}

	mu sync.RWMutex
}

func CreateSession() *Session {
	return &Session{
		caption:      nil,
		notification: make(chan struct{}),
	}
}

func (s *Session) NewCaption() {
	c := &protocol.SourceHello{
		Type:    protocol.TypeSourceHello,
		Session: "test",
	}
	s.mu.Lock()
	s.caption = c
	close(s.notification)
	s.notification = make(chan struct{})
	s.mu.Unlock()
}

// Helper struct
type Snapshot struct {
	caption      *protocol.SourceHello
	notification <-chan struct{}
}

func (s *Session) GetSnapshot() Snapshot {
	s.mu.RLock()
	caption := s.caption
	notification := s.notification
	s.mu.RUnlock()
	return Snapshot{
		caption:      caption,
		notification: notification,
	}
}
