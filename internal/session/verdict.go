package session

type Verdict int

const (
	// Accepted: the update was stored and every waiting client was woken.
	Accepted Verdict = iota + 1
	// Duplicate: same revision as the one already stored in the segment.
	Duplicate
	// Stale: lower revision than thet one already stored for the segment.
	Stale
	// AlreadyFinal: the segment is already final, so the update was ignored.
	AlreadyFinal
	// TooOld: the segment is at or below the highest segment whose final has been evicted from the ring
	TooOld
)

func (v Verdict) String() string {
	switch v {
	case Accepted:
		return "Accepted"
	case Duplicate:
		return "Duplicate"
	case Stale:
		return "Stale"
	case AlreadyFinal:
		return "AlreadyFinal"
	case TooOld:
		return "TooOld"
	}
	return "Verdict(0)"
}
