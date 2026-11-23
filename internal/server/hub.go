package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// LeadTime is how far in the future a newly selected video starts, giving
// every phone time to buffer so they all begin together.
const LeadTime = 1500 * time.Millisecond

// State is the single source of truth broadcast to every phone.
type State struct {
	Stopped bool   `json:"stopped"`
	VideoID string `json:"videoId,omitempty"`
	URL     string `json:"url,omitempty"`
	Name    string `json:"name,omitempty"`
	StartAt int64  `json:"startAt,omitempty"` // server epoch ms
}

type client struct {
	send chan []byte
	conn *websocket.Conn
}

// Hub tracks connected phones and the current playback state.
type Hub struct {
	now     func() time.Time
	mu      sync.Mutex
	state   State
	clients map[*client]struct{}
	onCount func(int)
}

func NewHub(now func() time.Time) *Hub {
	if now == nil {
		now = time.Now
	}
	return &Hub{now: now, state: State{Stopped: true}, clients: map[*client]struct{}{}}
}

// httpHandlerFunc adapts a method value to http.Handler (used by tests).
type httpHandlerFunc func(http.ResponseWriter, *http.Request)

func (f httpHandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) { f(w, r) }

func stateMsg(s State) []byte {
	b, _ := json.Marshal(struct {
		Type  string `json:"type"`
		State State  `json:"state"`
	}{"state", s})
	return b
}

// Play switches every phone to the given video, starting LeadTime from now.
func (h *Hub) Play(videoID, name, url string) {
	h.set(State{VideoID: videoID, Name: name, URL: url, StartAt: h.now().Add(LeadTime).UnixMilli()})
}

// Stop blanks every phone.
func (h *Hub) Stop() { h.set(State{Stopped: true}) }

func (h *Hub) set(s State) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state = s
	msg := stateMsg(s)
	for c := range h.clients {
		h.enqueueLocked(c, msg)
	}
}

// enqueueLocked queues msg for c, dropping a client whose buffer is full so a
// slow phone never blocks the others. Caller holds h.mu.
func (h *Hub) enqueueLocked(c *client, msg []byte) {
	select {
	case c.send <- msg:
	default:
		h.removeLocked(c)
		c.conn.CloseNow()
	}
}

func (h *Hub) State() State {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}

func (h *Hub) Count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// OnCount registers a callback invoked whenever the client count changes.
// It runs while the hub lock is held, so it must be quick and must not call
// back into the Hub (hand off with fyne.Do or a channel).
func (h *Hub) OnCount(fn func(int)) {
	h.mu.Lock()
	h.onCount = fn
	h.mu.Unlock()
}

func (h *Hub) notifyLocked() {
	if h.onCount != nil {
		fn, n := h.onCount, len(h.clients)
		fn(n)
	}
}

func (h *Hub) removeLocked(c *client) {
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		close(c.send)
		h.notifyLocked()
	}
}

// ServeWS upgrades the request and serves one phone.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	c := &client{send: make(chan []byte, 16), conn: conn}

	h.mu.Lock()
	h.clients[c] = struct{}{}
	c.send <- stateMsg(h.state)
	h.notifyLocked()
	h.mu.Unlock()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer cancel()
		for msg := range c.send {
			wctx, wcancel := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Write(wctx, websocket.MessageText, msg)
			wcancel()
			if err != nil {
				return
			}
		}
	}()

	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			break
		}
		var in struct {
			Type string  `json:"type"`
			T0   float64 `json:"t0"`
		}
		if json.Unmarshal(b, &in) != nil || in.Type != "ping" {
			continue
		}
		out, _ := json.Marshal(struct {
			Type string  `json:"type"`
			T0   float64 `json:"t0"`
			TS   int64   `json:"ts"`
		}{"pong", in.T0, h.now().UnixMilli()})
		h.mu.Lock()
		if _, ok := h.clients[c]; ok {
			h.enqueueLocked(c, out)
		}
		h.mu.Unlock()
	}

	h.mu.Lock()
	h.removeLocked(c)
	h.mu.Unlock()
	conn.CloseNow()
}
