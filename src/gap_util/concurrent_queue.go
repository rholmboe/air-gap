package gap_util

import (
	"sync"
)

type Item struct {
	ID      string
	Topic   string
	Message []byte
}

type Queue struct {
	items []Item
	lock  sync.RWMutex
}

func (q *Queue) Enqueue(item Item) {
	q.lock.Lock()
	q.items = append(q.items, item)
	q.lock.Unlock()
}

func (q *Queue) Dequeue() *Item {
	q.lock.Lock()
	defer q.lock.Unlock()

	if len(q.items) == 0 {
		return nil
	}

	item := q.items[0]
	q.items = q.items[1:]
	return &item
}

func (q *Queue) IsEmpty() bool {
	q.lock.RLock()
	defer q.lock.RUnlock()

	return len(q.items) == 0
}
