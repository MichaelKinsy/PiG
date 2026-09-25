package angrypigs

import (
	"image/color"
	"math"
)

type particleKind uint8

const (
	// dustKind drifts up and fades: impact puffs and landing dust.
	dustKind particleKind = iota
	// debrisKind falls with gravity and bounces once: broken block chunks.
	debrisKind
	// featherKind sways as it sinks slowly: knocked-out birds.
	featherKind
)

type particle struct {
	x, y, vx, vy float64
	age, life    float64
	col          color.RGBA
	kind         particleKind
	bounced      bool
}

// particles is a fixed-capacity pool. A burst reuses dead slots and, when the
// pool is full, replaces the oldest particles, so effects never allocate.
type particles struct {
	pool [maxParticles]particle
	next int
	live int
	// used is how many leading slots have ever held a particle since the
	// last clear; slots past it are empty and are not visited.
	used int
	seed uint32
}

const maxParticles = 256

func (p *particles) clear() {
	p.pool = [maxParticles]particle{}
	p.live, p.used, p.next = 0, 0, 0
}

// rand returns a deterministic pseudo-random value in [0, 1).
func (p *particles) rand() float64 {
	p.seed = p.seed*1664525 + 1013904223
	return float64(p.seed>>8) / (1 << 24)
}

// burst emits n particles at (x, y) that scatter up to speed pixels per
// second and live about life seconds.
func (p *particles) burst(x, y float64, n int, kind particleKind, col color.RGBA, speed, life float64) {
	for range n {
		angle := p.rand() * 2 * math.Pi
		v := speed * (0.35 + 0.65*p.rand())
		slot := &p.pool[p.next]
		if slot.age >= slot.life {
			p.live++
		}
		*slot = particle{
			x: x, y: y,
			vx: math.Cos(angle) * v, vy: math.Abs(math.Sin(angle))*v*0.9 + speed*0.15,
			life: life * (0.6 + 0.4*p.rand()),
			col:  col, kind: kind,
		}
		p.next = (p.next + 1) % maxParticles
		p.used = max(p.used, p.next)
		if p.next == 0 {
			p.used = maxParticles
		}
	}
}

func (p *particles) step(dt float64) {
	if p.live == 0 {
		return
	}
	live := 0
	for i := range p.used {
		q := &p.pool[i]
		if q.age >= q.life {
			continue
		}
		q.age += dt
		switch q.kind {
		case dustKind:
			q.vx *= 1 - 3*dt
			q.vy = q.vy*(1-3*dt) + 6*dt
		case debrisKind:
			q.vy -= gravity * dt
			if q.y <= 0 && q.vy < 0 {
				if q.bounced {
					q.vx, q.vy = 0, 0
				} else {
					q.vy, q.vx, q.bounced = -q.vy*0.35, q.vx*0.5, true
				}
				q.y = 0
			}
		case featherKind:
			q.vx = q.vx*(1-2*dt) + math.Sin(q.age*9)*20*dt
			q.vy = max(q.vy-40*dt, -10)
		}
		q.x += q.vx * dt
		q.y = max(q.y+q.vy*dt, 0)
		if q.age < q.life {
			live++
		}
	}
	p.live = live
	if live == 0 {
		p.used, p.next = 0, 0
	}
}
