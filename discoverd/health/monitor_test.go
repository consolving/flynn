package health

import (
	"errors"
	"time"

	. "github.com/flynn/go-check"
	"github.com/flynn/flynn/pkg/stream"
)

type MonitorSuite struct{}

var _ = Suite(&MonitorSuite{})

type CheckFunc func() error

func (f CheckFunc) Check() error { return f() }

func (MonitorSuite) TestMonitor(c *C) {
	type step struct {
		up    bool
		event MonitorStatus
	}

	checker := func(steps []step, threshold int) (chan MonitorEvent, chan MonitorEvent, stream.Stream) {
		var i int
		var finished bool
		expectedEvents := make(chan MonitorEvent, 1)
		actualEvents := make(chan MonitorEvent)

		check := CheckFunc(func() error {
			if finished {
				return errors.New("finished")
			}
			defer func() {
				if i >= len(steps) {
					finished = true
					close(expectedEvents)
				}
			}()

			step := steps[i]
			i++

			if !step.up {
				err := errors.New("check failure")
				if step.event > 0 {
					expectedEvents <- MonitorEvent{
						Status: step.event,
						Err:    err,
					}
				}
				return err
			}
			if step.event > 0 {
				expectedEvents <- MonitorEvent{Status: step.event}
			}
			return nil
		})

		s := Monitor{
			Threshold:     threshold,
			StartInterval: time.Nanosecond,
			Interval:      time.Nanosecond,
		}.Run(check, actualEvents)
		return expectedEvents, actualEvents, s
	}

	for _, t := range []struct {
		name      string
		steps     []step
		threshold int
	}{
		{
			name:  "service doesn't come up",
			steps: []step{{}, {}, {}},
		},
		{
			name: "service comes up right away",
			steps: []step{
				{event: MonitorStatusUp, up: true},
				{up: true},
				{up: true},
			},
		},
		{
			name: "service comes up after a few checks",
			steps: []step{
				{}, {}, {},
				{event: MonitorStatusUp, up: true},
			},
		},
		{
			name: "up/down/up - default threshold",
			steps: []step{
				{event: MonitorStatusUp, up: true},
				{},
				{event: MonitorStatusDown},
				{up: true},
				{event: MonitorStatusUp, up: true},
			},
		},
		{
			name:      "up/down/up - custom threshold",
			threshold: 3,
			steps: []step{
				{event: MonitorStatusUp, up: true},
				{},
				{},
				{event: MonitorStatusDown},
				{up: true},
				{up: true},
				{event: MonitorStatusUp, up: true},
			},
		},
		{
			name: "flapping - alternate",
			steps: []step{
				{event: MonitorStatusUp, up: true},
				{},
				{up: true},
				{},
				{up: true},
				{},
				{event: MonitorStatusDown},
				{up: true},
				{},
				{up: true},
				{},
			},
		},
		{
			name:      "flapping - consecutive",
			threshold: 3,
			steps: []step{
				{event: MonitorStatusUp, up: true},
				{},
				{},
				{up: true},
				{},
				{},
				{up: true},
				{},
				{},
				{event: MonitorStatusDown},
				{up: true},
				{up: true},
				{},
				{up: true},
				{up: true},
				{},
			},
		},
	} {
		c.Log(t.name)

		expectedEvents, actualEvents, s := checker(t.steps, t.threshold)
		for expected := range expectedEvents {
			actual := <-actualEvents
			// functions are not comparable, so we check it and then nil it
			c.Assert(actual.Check, FitsTypeOf, CheckFunc(nil))
			actual.Check = nil
			c.Assert(actual, DeepEquals, expected)
		}
		s.Close()
	}
}
