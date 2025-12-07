package sync

import (
	"sync"
)

type Broadcaster struct {
	subscribers map[string]map[chan struct{}]bool
	lock        sync.RWMutex
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subscribers: make(map[string]map[chan struct{}]bool),
	}
}

func (b *Broadcaster) Subscribe(topic string) chan struct{} {
	ch := make(chan struct{}, 1)
	b.lock.Lock()
	defer b.lock.Unlock()

	if _, ok := b.subscribers[topic]; !ok {
		b.subscribers[topic] = make(map[chan struct{}]bool)
	}
	b.subscribers[topic][ch] = true
	return ch
}

func (b *Broadcaster) Unsubscribe(topic string, ch chan struct{}) {
	b.lock.Lock()
	defer b.lock.Unlock()

	if subs, ok := b.subscribers[topic]; ok {
		delete(subs, ch)
		if len(subs) == 0 {
			delete(b.subscribers, topic)
		}
	}
	close(ch)
}

func (b *Broadcaster) Notify(topic string) {
	b.lock.RLock()
	defer b.lock.RUnlock()

	if subs, ok := b.subscribers[topic]; ok {
		for ch := range subs {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}
