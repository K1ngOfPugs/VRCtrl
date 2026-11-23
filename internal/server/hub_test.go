package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

var fixedNow = time.UnixMilli(1_700_000_000_000)

func newTestHub(t *testing.T) (*Hub, *httptest.Server) {
	t.Helper()
	h := NewHub(func() time.Time { return fixedNow })
	srv := httptest.NewServer(httpHandlerFunc(h.ServeWS))
	t.Cleanup(srv.Close)
	return h, srv
}

func dial(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

type msg struct {
	Type  string  `json:"type"`
	State State   `json:"state"`
	T0    float64 `json:"t0"`
	TS    int64   `json:"ts"`
}

func read(t *testing.T, c *websocket.Conn) msg {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, b, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var m msg
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func waitCount(t *testing.T, h *Hub, n int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if h.Count() == n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("count = %d, want %d", h.Count(), n)
}

func TestHubStateOnJoin(t *testing.T) {
	h, srv := newTestHub(t)
	c := dial(t, srv)
	if m := read(t, c); m.Type != "state" || !m.State.Stopped {
		t.Fatalf("initial = %+v", m)
	}
	h.Play("abc", "a.mp4", "/video/abc")
	read(t, c)
	c2 := dial(t, srv)
	m := read(t, c2)
	want := fixedNow.UnixMilli() + 1500
	if m.Type != "state" || m.State.VideoID != "abc" || m.State.URL != "/video/abc" || m.State.StartAt != want || m.State.Stopped {
		t.Fatalf("join state = %+v, want startAt %d", m, want)
	}
}

func TestHubBroadcastToAll(t *testing.T) {
	h, srv := newTestHub(t)
	a, b := dial(t, srv), dial(t, srv)
	read(t, a)
	read(t, b)
	waitCount(t, h, 2)
	h.Play("x", "x.mp4", "/video/x")
	for _, c := range []*websocket.Conn{a, b} {
		if m := read(t, c); m.State.VideoID != "x" {
			t.Fatalf("got %+v", m)
		}
	}
}

func TestHubPingPong(t *testing.T) {
	_, srv := newTestHub(t)
	c := dial(t, srv)
	read(t, c)
	ctx := context.Background()
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"ping","t0":123.5}`)); err != nil {
		t.Fatal(err)
	}
	m := read(t, c)
	if m.Type != "pong" || m.T0 != 123.5 || m.TS != fixedNow.UnixMilli() {
		t.Fatalf("pong = %+v", m)
	}
}

func TestHubCountAndOnCount(t *testing.T) {
	h, srv := newTestHub(t)
	var mu sync.Mutex
	var seen []int
	h.OnCount(func(n int) { mu.Lock(); seen = append(seen, n); mu.Unlock() })
	a := dial(t, srv)
	read(t, a)
	waitCount(t, h, 1)
	b := dial(t, srv)
	read(t, b)
	waitCount(t, h, 2)
	a.Close(websocket.StatusNormalClosure, "")
	waitCount(t, h, 1)
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 3 || seen[0] != 1 || seen[1] != 2 || seen[2] != 1 {
		t.Fatalf("seen = %v", seen)
	}
}

func TestHubStopBroadcast(t *testing.T) {
	h, srv := newTestHub(t)
	c := dial(t, srv)
	read(t, c)
	h.Play("x", "x.mp4", "/video/x")
	read(t, c)
	h.Stop()
	if m := read(t, c); !m.State.Stopped || m.State.VideoID != "" {
		t.Fatalf("stop = %+v", m)
	}
	if !h.State().Stopped {
		t.Fatal("State() not stopped")
	}
}

// Many clients churning while Play is called must not deadlock or panic.
func TestHubChurn(t *testing.T) {
	h, srv := newTestHub(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if err != nil {
				return
			}
			c.CloseNow()
		}()
	}
	for i := 0; i < 50; i++ {
		h.Play("x", "x", "/video/x")
	}
	wg.Wait()
	waitCount(t, h, 0)
}
