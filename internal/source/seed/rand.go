package seed

// rng is mulberry32, the generator the original prototype used.
//
// It is reproduced exactly — including the 32-bit wraparound — so that the Go
// dashboard and the JavaScript prototype produce identical values from the same
// seed. That fidelity is worth having: it makes the port verifiable rather than
// merely plausible.
//
// The arithmetic is uint32 throughout, which reproduces JavaScript's Math.imul
// and >>> semantics without any masking.
type rng struct{ state uint32 }

func newRNG(seed uint32) *rng { return &rng{state: seed} }

// float returns the next value in [0,1).
func (r *rng) float() float64 {
	r.state += 0x6D2B79F5
	s := r.state
	t := (s ^ (s >> 15)) * (1 | s)
	t = (t + ((t ^ (t >> 7)) * (61 | t))) ^ t
	return float64(t^(t>>14)) / 4294967296.0
}

// between returns the next value scaled into [lo,hi).
func (r *rng) between(lo, hi float64) float64 { return lo + r.float()*(hi-lo) }

// intBetween returns the next value as a rounded integer.
func (r *rng) intBetween(lo, hi float64) int64 { return int64(r.between(lo, hi) + 0.5) }

// pick chooses one element deterministically. An empty slice yields the zero
// value, so callers need no length check.
func pick[T any](r *rng, items []T) T {
	var zero T
	if len(items) == 0 {
		return zero
	}
	return items[int(r.float()*float64(len(items)))]
}
