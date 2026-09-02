package main

import (
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"

	"github.com/flynn/flynn/discoverd/client"
	"github.com/flynn/flynn/pkg/shutdown"
)

/*
	ish: the Inexusable/Insecure/Internet SHell.
*/
func main() {
	defer shutdown.Exit()

	name := os.Getenv("NAME")
	port := os.Getenv("PORT")
	addr := ":" + port
	if name == "" {
		name = "ish-service"
	}

	l, err := net.Listen("tcp", addr)
	if err != nil {
		shutdown.Fatal(err)
	}
	defer l.Close()

	hb, err := discoverd.AddServiceAndRegister(name, addr)
	if err != nil {
		shutdown.Fatal(err)
	}
	shutdown.BeforeExit(func() { hb.Close() })

	// token must be set explicitly; the server refuses to run without it so
	// the endpoint can never be an open, unauthenticated command shell.
	token := os.Getenv("TOKEN")
	if token == "" {
		shutdown.Fatal(errors.New("ish: TOKEN environment variable is required"))
	}

	http.HandleFunc("/ish", func(w http.ResponseWriter, r *http.Request) {
		ish(w, r, token)
	})
	if err := http.Serve(l, nil); err != nil {
		shutdown.Fatal(err)
	}
}

func ish(resp http.ResponseWriter, req *http.Request, token string) {
	if req.Method != "POST" {
		http.Error(resp, "405 Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	// require a shared secret so the command-execution endpoint is not open
	// to unauthenticated callers.
	if req.Header.Get("Authorization") != "Bearer "+token {
		http.Error(resp, "401 Unauthorized", http.StatusUnauthorized)
		return
	}
	body, _ := io.ReadAll(req.Body)

	cmd := exec.Command("/bin/sh", "-c", string(body)) // no bash in busybox
	cmd.Stdout = io.MultiWriter(resp, os.Stdout)
	cmd.Stderr = io.MultiWriter(resp, os.Stderr)
	cmd.Run()
}
