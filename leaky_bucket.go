package pioncc

import (
	"math"
	"time"
)

type leakyBucket struct {
	burstSize  int
	rate       int
	lastSent   time.Time
	lastBudget int
}

func (b *leakyBucket) setRate(rate int) {
	b.rate = rate
}

func (b *leakyBucket) onSent(t time.Time, size int) {
	budget := b.budget(t)
	if size > budget {
		b.lastBudget = 0
	} else {
		b.lastBudget = budget - size
	}
	b.lastSent = t
}

func (b *leakyBucket) budget(t time.Time) int {
	if b.lastSent.IsZero() {
		return b.burstSize
	}
	td := t.Sub(b.lastSent)
	budget := b.lastBudget + 8*int(float64(b.rate)*td.Seconds())
	if budget < 0 {
		budget = math.MaxInt
	}
	budget = min(budget, b.burstSize)
	return budget
}
