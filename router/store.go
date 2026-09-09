package main

import (
	"encoding/json"
	"errors"

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

// call resolves the live controller instances for each invocation and
// invokes fn with a client for the first instance that accepts the
// request. Unlike pinning to the first instance at startup, this allows
// the router to keep syncing when the previously used controller is
// replaced or dies (the controllers and the router are restarted
// independently, so a restarted router must not assume the original
// instance is still in discoverd).
func (c *ControllerStore) call(fn func(controller.Client) error) error {
	instances, err := c.service.Instances()
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		return errors.New("router: no controller instances available")
	}
	var lastErr error
	for _, inst := range instances {
		client, err := controller.NewClient("http://"+inst.Addr, inst.Meta["AUTH_KEY"])
		if err != nil {
			lastErr = err
			continue
		}
		if err := fn(client); err != nil {
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
	)
	err := c.call(func(client controller.Client) error {
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
	go func() {
		defer close(ch)
		defer eventStream.Close()
		for {
			select {
			case event, ok := <-events:
				if !ok {
					routeStream.Error = eventStream.Err()
					return
				}
				var route router.Route
				if err := json.Unmarshal(event.Data, &route); err != nil {
					routeStream.Error = err
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
