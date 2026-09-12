package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	discoverd "github.com/flynn/flynn/discoverd/client"
	"github.com/flynn/flynn/discoverd/testutil"
	router "github.com/flynn/flynn/router/types"
)

// fakeController stands in for a controller instance: it serves /routes
// and a long-lived /events SSE stream (which it never actually sends
// events on). Shutdown closes the server, simulating the controller
// instance dying.
type fakeController struct {
	srv *httptest.Server

	mu       sync.Mutex
	sawEvent bool

	done chan struct{}
	once sync.Once
}

func newFakeController(t *testing.T) *fakeController {
	f := &fakeController{done: make(chan struct{})}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/routes":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("[]"))
		case "/events":
			f.mu.Lock()
			f.sawEvent = true
			f.mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if fl, ok := w.(http.Flusher); ok {
				fl.Flush()
			}
			// hold the connection open until the client goes away or
			// the fake is shut down
			select {
			case <-f.done:
			case <-r.Context().Done():
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	return f
}

// Shutdown terminates any in-flight /events handlers and closes the
// server.
func (f *fakeController) Shutdown() {
	f.once.Do(func() { close(f.done) })
	f.srv.Close()
}

func (f *fakeController) sawEventStream(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		saw := f.sawEvent
		f.mu.Unlock()
		if saw {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the controller event stream to be established")
}

func waitForControllerInstance(t *testing.T, service discoverd.Service, addr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		instances, err := service.Instances()
		if err == nil {
			for _, inst := range instances {
				if inst.Addr == addr {
					return
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for controller instance %s to register", addr)
}

// TestControllerStoreFailover verifies that when the controller instance a
// route event stream is pinned to is replaced or dies, the store detects
// it via discoverd (instead of the built-in stream resumption silently
// retrying the dead address forever) and the sync fails so the listener
// re-resolves a live controller and re-syncs.
func TestControllerStoreFailover(t *testing.T) {
	dc, killDiscoverd := testutil.BootDiscoverd(t, "")
	defer killDiscoverd()

	store := &ControllerStore{service: dc.Service("controller")}

	// controller instance A
	ctlA := newFakeController(t)
	defer ctlA.Shutdown()
	hbA, err := dc.AddServiceAndRegister("controller", ctlA.srv.Listener.Addr().String())
	if err != nil {
		t.Fatal("registering controller instance A:", err)
	}
	defer hbA.Close()
	waitForControllerInstance(t, store.service, ctlA.srv.Listener.Addr().String())

	// open the route event stream (pins to instance A)
	ch := make(chan *router.Event, 8)
	stream, err := store.Watch(ch)
	if err != nil {
		t.Fatal("watching controller events:", err)
	}
	defer stream.Close()
	ctlA.sawEventStream(t)

	// kill instance A out from under the stream: the built-in resumption
	// keeps retrying the dead address, so the stream stays "open" (the
	// zombie state this test guards against)...
	ctlA.Shutdown()
	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("unexpected event after the controller instance died: %+v", ev)
		}
		t.Fatal("event stream failed over before the instance left discoverd")
	case <-time.After(200 * time.Millisecond):
	}

	// ...until discoverd deregisters it, which must fail the stream so
	// the listener re-syncs from a live controller.
	if err := hbA.Close(); err != nil {
		t.Fatal("deregistering controller instance A:", err)
	}
	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("unexpected event while failing over: %+v", ev)
		}
		// channel closed: the failover succeeded
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for the controller event stream to fail over")
	}
	if err := stream.Err(); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Fatalf("unexpected stream error after failover: %v", err)
	}

	// a resync now resolves the replacement instance B
	ctlB := newFakeController(t)
	defer ctlB.Shutdown()
	hbB, err := dc.AddServiceAndRegister("controller", ctlB.srv.Listener.Addr().String())
	if err != nil {
		t.Fatal("registering controller instance B:", err)
	}
	defer hbB.Close()
	waitForControllerInstance(t, store.service, ctlB.srv.Listener.Addr().String())

	ch2 := make(chan *router.Event, 8)
	stream2, err := store.Watch(ch2)
	if err != nil {
		t.Fatal("re-watching controller events:", err)
	}
	defer stream2.Close()
	ctlB.sawEventStream(t)
}
