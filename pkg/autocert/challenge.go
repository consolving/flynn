package autocert

import (
	"net/http"
	"strings"
	"sync"
)

// HTTP01Provider implements lego's challenge.Provider interface for HTTP-01.
// It stores challenge responses in memory and exposes an http.Handler that
// serves them at /.well-known/acme-challenge/<token>.
type HTTP01Provider struct {
	mu      sync.Mutex
	answers map[string]string // token -> keyAuth
}

// NewHTTP01Provider returns a new HTTP-01 challenge provider.
func NewHTTP01Provider() *HTTP01Provider {
	return &HTTP01Provider{
		answers: make(map[string]string),
	}
}

// Present stores the challenge response so it can be served.
func (p *HTTP01Provider) Present(domain, token, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.answers[token] = keyAuth
	return nil
}

// CleanUp removes the challenge response.
func (p *HTTP01Provider) CleanUp(domain, token, keyAuth string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.answers, token)
	return nil
}

// ServeHTTP serves HTTP-01 challenge responses. Mount at
// /.well-known/acme-challenge/.
func (p *HTTP01Provider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/.well-known/acme-challenge/")
	token = strings.Trim(token, "/")

	p.mu.Lock()
	auth, ok := p.answers[token]
	p.mu.Unlock()

	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(auth))
}
