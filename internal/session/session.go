package session

import (
	"time"
	"sync"
)

// Config holds the configuration for a session.
type Config struct {
	// Retention: finals whose source_ts is older than the newest source_ts
	// seen minus Retention are evicted. Must exceed the 60 s HISTORY maximum.
	Retention time.Duration
	// MaxFinals is the ring capacity: at most this many finals are kept.
	MaxFinals int
	// MaxActive is the most provisional (not yet final) segments tracked at
	// once. When full, the lowest-numbered active segment is evicted.
	MaxActive int
}

// DefaultConfig returns the configuration used when the evaluater starts.
func DefaultConfig() Config {
	return Config{
		Retention:  time.Second * 90,
		MaxFinals:  1024,
		MaxActive:  16,
	}
}

// Session is the master window, create with New, only 1 Session per instance
type Session struct {
	cfg Config
	// mu protects all fields below.
	mu sync.RWMutex

	// finals holds the most recent finals, in a ring buffer. 
	// The oldest final is at oldestSeq, the newest at newestSeq.
	finals ring
	// finalized maps segment -> fseq of the final in finals, if any.
	finalized map[int64]uint64
	// active holds the newest provisional of each segment that has no final
	// yet. At most cfg.MaxActive entries.
	active map[int64]*caption
	// floor is the highest segment whose final has been evicted from finals.
	// Any update for a segment <= floor is TooOld.
	floor int64
	// newestTS is the timestamp of the newest update ever seen, final or not.
	// It is the only clock the session usesm for eviction and the HISTORY window
	newestTS float64
	// changed is closed and replaced on every Accepted update. 
	// Pick hands the current one to each client to wait on
	changed chan struct{}
}

// New returns an empty session with a logger. Zero fields in cfg are replaced with DefaultConfig values.
func New(cfg Config) *Session {
	def := DefaultConfig()
	if cfg.Retention == 0 {
		cfg.Retention = def.Retention
	}
	if cfg.MaxFinals == 0 {
		cfg.MaxFinals = def.MaxFinals
	}
	if cfg.MaxActive == 0 {
		cfg.MaxActive = def.MaxActive
	}
	return &Session{
		cfg: 		cfg,
		finals: 	newRing(cfg.MaxFinals),
		finalized: 	make(map[int64]uint64),
		active: 	make(map[int64]*caption),
		changed: 	make(chan struct{}),
	}
}

// wakeAll wakes every client waiting on the current changed channel and
// installs a fresh one for the next update. Closing a channel wakes all its
// receivers at once, so this is O(1) however many clients there are.
// The caller must hold s.mu for writing.
func (s *Session) wakeAll() {
	close(s.changed)
	s.changed = make(chan struct{})
}