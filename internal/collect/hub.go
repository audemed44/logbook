package collect

import (
	"sync"

	"github.com/audemed44/logbook/internal/store"
)

// Hub passes stored lines to live tails. A tail that falls behind loses
// lines rather than holding up the writer; it's told so it can reload.
type Hub struct {
	mu   sync.Mutex
	subs map[*Sub]struct{}
}

type Sub struct {
	C      chan store.Line
	Missed chan struct{} // gets a value when lines were dropped for this tail
}

func NewHub() *Hub { return &Hub{subs: map[*Sub]struct{}{}} }

// maxTails bounds open live tails (each holds a buffer).
const maxTails = 16

// Subscribe opens a tail, or returns nil when there are too many.
func (h *Hub) Subscribe() *Sub {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) >= maxTails {
		return nil
	}
	s := &Sub{C: make(chan store.Line, 256), Missed: make(chan struct{}, 1)}
	h.subs[s] = struct{}{}
	return s
}

func (h *Hub) Unsubscribe(s *Sub) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

func (h *Hub) Publish(lines []store.Line) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		for _, l := range lines {
			select {
			case s.C <- l:
			default:
				select {
				case s.Missed <- struct{}{}:
				default:
				}
			}
		}
	}
}
