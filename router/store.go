package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	controller "github.com/flynn/flynn/controller/client"
	ct "github.com/flynn/flynn/controller/types"
	discoverd "github.com/flynn/flynn/discoverd/client"
	"github.com/flynn/flynn/pkg/stream"
	router "github.com/flynn/flynn/router/types"
)

type Store interface {
	List() ([]*router.Route, error)
	Watch(ch chan *router.Event) (stream.Stream, error)
}

func NewControllerStore() (*ControllerStore, error) {
	return &ControllerStore{service: discoverd.NewService("controller")}, nil
}

type ControllerStore struct {
	service discoverd.Service
}

// callInst resolves the live controller instances for each invocation and
// invokes fn with the instance and a client for the first instance that
// accepts the request. Unlike pinning to the first instance at startup,
// this allows the router to keep syncing when the previously used
// controller is replaced or dies (the controllers and the router are
// restarted independently, so a restarted router must not assume the
// original instance is still in discoverd).
func (c *ControllerStore) callInst(fn func(inst *discoverd.Instance, client controller.Client) error) error {
	instances, err := c.service.Instances()
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		return errors.New("router: no controller instances available")
	}
	var lastErr error
	for _, inst := range instances {
		client, err := newControllerClient(inst)
		if err != nil {
			lastErr = err
			continue
		}
		if err := fn(inst, client); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		return errors.New("router: no reachable controller instances")
	}
	return lastErr
}

func (c *ControllerStore) call(fn func(controller.Client) error) error {
	return c.callInst(func(_ *discoverd.Instance, client controller.Client) error {
		return fn(client)
	})
}

// newControllerClient builds a controller client for the given instance.
// The connect timeout bounds how long a dead instance can stall callInst
// before it moves on to the next live one; it only applies to establishing
// the connection, so long-lived event streams are unaffected.
func newControllerClient(inst *discoverd.Instance) (controller.Client, error) {
	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		},
	}
	return controller.NewClientWithHTTP("http://"+inst.Addr, inst.Meta["AUTH_KEY"], httpClient)
}

func (c *ControllerStore) List() ([]*router.Route, error) {
	var routes []*router.Route
	err := c.call(func(client controller.Client) error {
		var err error
		routes, err = client.RouteList()
		return err
	})
	return routes, err
}

func (c *ControllerStore) Watch(ch chan *router.Event) (stream.Stream, error) {
	var (
		events      chan *ct.Event
		eventStream stream.Stream
		ourAddr     string
	)
	err := c.callInst(func(inst *discoverd.Instance, client controller.Client) error {
		ourAddr = inst.Addr
		events = make(chan *ct.Event)
		var err error
		eventStream, err = client.StreamEvents(ct.StreamEventsOptions{
			ObjectTypes: []ct.EventType{
				ct.EventTypeRoute,
				ct.EventTypeRouteDeletion,
			},
		}, events)
		return err
	})
	if err != nil {
		return nil, err
	}
	routeStream := stream.New()

	// routeStream.Error may be written by both the instance monitor below
	// and the forwarding goroutine; guard it. The reader (Syncer.Sync)
	// only reads it after the event channel has closed, which happens
	// after all of these writes.
	var (
		errMu  sync.Mutex
		setErr = func(e error) {
			errMu.Lock()
			routeStream.Error = e
			errMu.Unlock()
		}
	)

	// The built-in stream resumption (pkg/httpclient.ResumingStream)
	// only retries against the instance it was first connected to. When
	// that controller instance is replaced or dies, discoverd expires
	// its registration and the event stream stops delivering updates,
	// while the resumption loop keeps retrying the dead address forever
	// and the route table silently goes stale. Watching the controller
	// service's instance events detects this: when the instance we are
	// streaming from goes away, force the stream to fail so Sync
	// returns and the listener re-resolves a live controller and
	// re-syncs from scratch (no events are lost: the initial route
	// list is re-fetched on resync).
	svcEvents := make(chan *discoverd.Event, 16)
	if svcStream, err := c.service.Watch(svcEvents); err == nil {
		go func() {
			defer svcStream.Close()
			for {
				select {
				case <-routeStream.StopCh:
					return
				case ev, ok := <-svcEvents:
					if !ok {
						return
					}
					if ev.Kind == discoverd.EventKindDown && ev.Instance != nil && ev.Instance.Addr == ourAddr {
						setErr(errors.New("router: controller instance " + ourAddr + " no longer available"))
						eventStream.Close()
						return
					}
				}
			}
		}()
	}
	// If the instance event stream is unavailable we simply lose
	// failover for this sync session; the next resync attempt
	// re-resolves instances on its own.

	go func() {
		defer close(ch)
		defer eventStream.Close()
		for {
			select {
			case event, ok := <-events:
				if !ok {
					// Preserve the error set by the instance monitor,
					// if any, otherwise report the stream's own error
					// (a stream stopped with Close() reports
					// stream.ErrClosed, which we don't want to mask
					// the monitor's more specific error with).
					if err := eventStream.Err(); err != nil && !errors.Is(err, stream.ErrClosed) {
						setErr(err)
					}
					return
				}
				var route router.Route
				if err := json.Unmarshal(event.Data, &route); err != nil {
					setErr(err)
					return
				}
				routerEvent := &router.Event{
					Event: c.toRouterEventType(event.ObjectType),
					ID:    route.ID,
					Route: &route,
				}
				select {
				case ch <- routerEvent:
				case <-routeStream.StopCh:
					return
				}
			case <-routeStream.StopCh:
				return
			}
		}
	}()
	return routeStream, nil
}

func (c *ControllerStore) toRouterEventType(typ ct.EventType) router.EventType {
	switch typ {
	case ct.EventTypeRoute:
		return router.EventTypeRouteSet
	case ct.EventTypeRouteDeletion:
		return router.EventTypeRouteRemove
	default:
		return router.EventType("")
	}
}
