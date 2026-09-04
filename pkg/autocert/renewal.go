package autocert

import (
	"context"
	"log"
	"sync"
	"time"
)

// Renewer runs periodic certificate renewal checks.
type Renewer struct {
	manager  *Manager
	interval time.Duration
	stop     chan struct{}
	done     sync.WaitGroup
}

// NewRenewer returns a Renewer for m that checks for renewal at interval.
// A zero interval defaults to 24 hours.
func NewRenewer(m *Manager, interval time.Duration) *Renewer {
	if interval == 0 {
		interval = 24 * time.Hour
	}
	return &Renewer{
		manager:  m,
		interval: interval,
		stop:     make(chan struct{}),
	}
}

// Start begins the renewal loop. It returns immediately; call Stop to shut it
// down.
func (r *Renewer) Start() {
	r.done.Add(1)
	go r.loop()
}

// Stop shuts down the renewal loop and waits for it to exit.
func (r *Renewer) Stop() {
	close(r.stop)
	r.done.Wait()
}

func (r *Renewer) loop() {
	defer r.done.Done()

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	// Run once at startup.
	r.renewAll()

	for {
		select {
		case <-ticker.C:
			r.renewAll()
		case <-r.stop:
			return
		}
	}
}

func (r *Renewer) renewAll() {
	certs, err := r.manager.store.ListCertificates()
	if err != nil {
		log.Printf("autocert: failed to list certificates for renewal: %v", err)
		return
	}
	for _, cert := range certs {
		if len(cert.Domains) == 0 || cert.ExpiresAt.IsZero() {
			continue
		}
		if time.Until(cert.ExpiresAt) > r.manager.config.RenewBefore {
			continue
		}
		log.Printf("autocert: renewing certificate for %v (expires %s)", cert.Domains, cert.ExpiresAt)
		if _, err := r.manager.Renew(cert); err != nil {
			log.Printf("autocert: failed to renew certificate for %v: %v", cert.Domains, err)
			continue
		}
		log.Printf("autocert: renewed certificate for %v", cert.Domains)
	}
}

// StartRenewal is a convenience helper that creates and starts a Renewer with
// the given interval. The returned Stop function blocks until the loop exits.
func (m *Manager) StartRenewal(ctx context.Context, interval time.Duration) context.CancelFunc {
	renewer := NewRenewer(m, interval)
	renewer.Start()
	cancel := context.CancelFunc(func() {
		renewer.Stop()
	})
	if ctx != nil {
		go func() {
			<-ctx.Done()
			renewer.Stop()
		}()
	}
	return cancel
}
