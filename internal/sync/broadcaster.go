package sync

import (
	"sync"
)

type Broadcaster struct {
	subscribers map[string]map[chan any]bool
	lock        sync.RWMutex
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{
		subscribers: make(map[string]map[chan any]bool),
	}
}

func (b *Broadcaster) Subscribe(topic string) chan any {
	ch := make(chan any, 1)
	b.lock.Lock()
	defer b.lock.Unlock()

	if _, ok := b.subscribers[topic]; !ok {
		b.subscribers[topic] = make(map[chan any]bool)
	}
	b.subscribers[topic][ch] = true
	return ch
}

func (b *Broadcaster) Unsubscribe(topic string, ch chan any) {
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

func (b *Broadcaster) Notify(topic string, data any) bool {
	b.lock.RLock()
	defer b.lock.RUnlock()

	subs, ok := b.subscribers[topic]
	if !ok || len(subs) == 0 {
		return false
	}

	for ch := range subs {
		select {
		case ch <- data:
		default:
		}
	}
	return true
}
