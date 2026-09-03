package main

import (
	"fmt"
	"os"
	"time"

	controller "github.com/flynn/flynn/controller/client"
	ct "github.com/flynn/flynn/controller/types"
)

func main() {
	endpoint := os.Getenv("FLYNN_CONTROLLER")
	key := os.Getenv("FLYNN_KEY")
	if endpoint == "" || key == "" {
		fmt.Fprintln(os.Stderr, "usage: FLYNN_CONTROLLER=http://host:port FLYNN_KEY=<api-key> event-debug")
		os.Exit(1)
	}
	client, err := controller.NewClient(endpoint, key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "client error: %v\n", err)
		os.Exit(1)
	}

	// Create an app
	app := &ct.App{}
	if err := client.CreateApp(app); err != nil {
		fmt.Fprintf(os.Stderr, "create app error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Created app: %s (%s)\n", app.Name, app.ID)

	// Get the app release (won't exist yet for a fresh app, so we need an artifact)
	// Let's just use StreamJobEvents directly on the app
	events := make(chan *ct.Job, 100)
	stream, err := client.StreamJobEvents(app.Name, events)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stream error: %v\n", err)
		os.Exit(1)
	}
	defer stream.Close()
	fmt.Println("Stream opened, waiting for events...")

	// Read events with timeout
	timeout := time.After(30 * time.Second)
	count := 0
	for {
		select {
		case e, ok := <-events:
			if !ok {
				fmt.Printf("Channel closed after %d events, stream err: %v\n", count, stream.Err())
				os.Exit(0)
			}
			count++
			fmt.Printf("Event %d: state=%s type=%s id=%s release=%s\n", count, e.State, e.Type, e.ID, e.ReleaseID)
		case <-timeout:
			fmt.Printf("Timeout after %d events\n", count)
			os.Exit(1)
		}
	}
}
