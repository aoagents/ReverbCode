package cdc

import "sync"

// Broadcaster is the in-process fan-out the consumer feeds. Subscribers (the
// WS/SSE transport, wired in the frontend task) register a callback; every
// consumed Event is delivered to all current subscribers. It is the single
// seam between the CDC pipeline and live delivery, so the transport can be
// built and swapped without touching the pipeline.
type Broadcaster struct {
	mu     sync.RWMutex
	nextID int
	subs   map[int]func(Event)
}

// NewBroadcaster returns an empty Broadcaster ready for subscriptions.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: map[int]func(Event){}}
}

// Subscribe registers fn and returns an unsubscribe function. fn is called
// synchronously from the consumer loop, so it must not block; a transport that
// needs buffering should push onto its own channel inside fn.
func (b *Broadcaster) Subscribe(fn func(Event)) (unsubscribe func()) {
	b.mu.Lock()
	id := b.nextID
	b.nextID++
	b.subs[id] = fn
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
	}
}

// Publish delivers e to every current subscriber. The subscriber slice is
// copied under RLock and the lock is released before any callback runs, so a
// slow or re-entrant subscriber (one that Subscribes/Unsubscribes from inside
// fn) cannot deadlock the broadcaster or block other publishers. Each callback
// is wrapped in a recover so one panicking subscriber does not disable fan-out
// to the rest.
func (b *Broadcaster) Publish(e Event) {
	b.mu.RLock()
	fns := make([]func(Event), 0, len(b.subs))
	for _, fn := range b.subs {
		fns = append(fns, fn)
	}
	b.mu.RUnlock()
	for _, fn := range fns {
		func() {
			defer func() {
				_ = recover()
			}()
			fn(e)
		}()
	}
}
