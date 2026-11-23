package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// LeadTime is how far in the future a newly selected video starts (or a
// paused one resumes), giving every phone time to buffer so they all begin
// together.
const LeadTime = 1500 * time.Millisecond

// maxSnap caps the size of one headset snapshot (decoded JPEG bytes).
const maxSnap = 256 << 10

// State is the single source of truth broadcast to every phone.
//
// Playback position is (now - StartAt), wrapped by the video's duration on the
// phone. Before PlayAt phones hold still at the position PlayAt will have.
// While Paused they hold at PausePos.
type State struct {
	Stopped  bool    `json:"stopped"`
	VideoID  string  `json:"videoId,omitempty"`
	URL      string  `json:"url,omitempty"`
	Name     string  `json:"name,omitempty"`
	StartAt  int64   `json:"startAt,omitempty"` // server epoch ms of virtual position 0
	PlayAt   int64   `json:"playAt,omitempty"`  // server epoch ms playback (re)starts
	Paused   bool    `json:"paused,omitempty"`
	PausePos float64 `json:"pausePos,omitempty"` // seconds (unwrapped)
}

// Position is where playback is (seconds, not wrapped by duration) at now.
func (s State) Position(now time.Time) float64 {
	switch {
	case s.Stopped:
		return 0
	case s.Paused:
		return s.PausePos
	}
	t := now.UnixMilli()
	if t < s.PlayAt {
		t = s.PlayAt
	}
	p := float64(t-s.StartAt) / 1000
	if p < 0 {
		return 0
	}
	return p
}

// Headset is what one phone last reported about itself.
type Headset struct {
	ID      string    // stable per phone (stored on the phone)
	Addr    string    // IP address
	Joined  time.Time // when this connection opened
	State   string    // idle, loading, waiting, playing, paused, buffering, tap, error
	VideoID string    // video the phone has loaded
	Pos     float64   // seconds
	Dur     float64   // seconds; 0 if unknown
	DriftMS float64   // actual - expected, ms (playing only)
	Cache   float64   // 1 = playing from its own saved copy, 0..1 = saving a copy, 0 = streaming
	Seen    time.Time // last status
	Snap    []byte    // latest JPEG of the left eye (may be nil)
	SnapAt  time.Time
	SnapVid string  // video the snapshot came from
	SnapPos float64 // position of the snapshot
}

type client struct {
	send chan []byte
	conn *websocket.Conn
	info Headset
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

// Play switches every phone to the given video from the beginning, starting
// LeadTime from now.
func (h *Hub) Play(videoID, name, url string) {
	at := h.now().Add(LeadTime).UnixMilli()
	h.set(State{VideoID: videoID, Name: name, URL: url, StartAt: at, PlayAt: at})
}

// Stop blanks every phone.
func (h *Hub) Stop() { h.set(State{Stopped: true}) }

// Pause freezes every phone on the current frame. No-op unless playing.
func (h *Hub) Pause() {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.state
	if s.Stopped || s.Paused {
		return
	}
	s.PausePos = s.Position(h.now())
	s.Paused = true
	h.setLocked(s)
}

// Resume continues from the paused frame, LeadTime from now. No-op unless paused.
func (h *Hub) Resume() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.state; !s.Stopped && s.Paused {
		h.setLocked(resumeAt(s, h.now(), s.PausePos))
	}
}

// TogglePause pauses when playing and resumes when paused.
func (h *Hub) TogglePause() {
	h.mu.Lock()
	paused := h.state.Paused
	h.mu.Unlock()
	if paused {
		h.Resume()
	} else {
		h.Pause()
	}
}

// Seek moves playback by delta seconds (clamped at 0). While playing, phones
// jump together LeadTime from now; while paused they show the new frame.
func (h *Hub) Seek(delta float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.state
	if s.Stopped {
		return
	}
	now := h.now()
	pos := s.Position(now.Add(LeadTime)) + delta
	if s.Paused {
		pos = s.PausePos + delta
	}
	if pos < 0 {
		pos = 0
	}
	if s.Paused {
		s.PausePos = pos
		h.setLocked(s)
		return
	}
	h.setLocked(resumeAt(s, now, pos))
}

func resumeAt(s State, now time.Time, pos float64) State {
	at := now.Add(LeadTime).UnixMilli()
	s.Paused, s.PausePos = false, 0
	s.PlayAt = at
	s.StartAt = at - int64(pos*1000)
	return s
}

func (h *Hub) set(s State) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.setLocked(s)
}

func (h *Hub) setLocked(s State) {
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

// Headsets returns what each connected phone last reported, one entry per
// phone ID (the newest connection wins), ordered by when they joined.
func (h *Hub) Headsets() []Headset {
	h.mu.Lock()
	byID := map[string]Headset{}
	for c := range h.clients {
		if old, ok := byID[c.info.ID]; !ok || c.info.Joined.After(old.Joined) {
			byID[c.info.ID] = c.info
		}
	}
	h.mu.Unlock()
	out := make([]Headset, 0, len(byID))
	for _, hs := range byID {
		out = append(out, hs)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Joined.Equal(out[j].Joined) {
			return out[i].Joined.Before(out[j].Joined)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// Identify shows label in big text on the headset with the given ID for a
// few seconds, so the operator can tell which physical headset it is.
func (h *Hub) Identify(id, label string) {
	msg, _ := json.Marshal(struct {
		Type  string `json:"type"`
		Label string `json:"label"`
	}{"identify", label})
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.info.ID == id {
			h.enqueueLocked(c, msg)
		}
	}
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

// inMsg is any message a phone sends.
type inMsg struct {
	Type  string  `json:"type"`
	T0    float64 `json:"t0"`
	State string  `json:"state"`
	Vid   string  `json:"vid"`
	Pos   float64 `json:"pos"`
	Dur   float64 `json:"dur"`
	Drift float64 `json:"drift"`
	Cache float64 `json:"cache"`
	JPEG  string  `json:"jpeg"` // base64
}

// ServeWS upgrades the request and serves one phone.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	conn.SetReadLimit(maxSnap*2 + 4096)
	id := r.URL.Query().Get("id")
	if len(id) > 64 {
		id = id[:64]
	}
	addr, _, _ := net.SplitHostPort(r.RemoteAddr)
	if id == "" {
		id = "anon-" + addr
	}
	c := &client{send: make(chan []byte, 16), conn: conn,
		info: Headset{ID: id, Addr: addr, Joined: h.now(), State: "idle", Seen: h.now()}}

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
		var in inMsg
		if json.Unmarshal(b, &in) != nil {
			continue
		}
		switch in.Type {
		case "ping":
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
		case "status":
			h.mu.Lock()
			c.info.State, c.info.VideoID = in.State, in.Vid
			c.info.Pos, c.info.Dur, c.info.DriftMS = in.Pos, in.Dur, in.Drift
			c.info.Cache = in.Cache
			c.info.Seen = h.now()
			h.mu.Unlock()
		case "snap":
			jpg, err := base64.StdEncoding.DecodeString(in.JPEG)
			if err != nil || len(jpg) == 0 || len(jpg) > maxSnap {
				continue
			}
			h.mu.Lock()
			c.info.Snap, c.info.SnapAt = jpg, h.now()
			c.info.SnapVid, c.info.SnapPos = in.Vid, in.Pos
			h.mu.Unlock()
		}
	}

	h.mu.Lock()
	h.removeLocked(c)
	h.mu.Unlock()
	conn.CloseNow()
}
