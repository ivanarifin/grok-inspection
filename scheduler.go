package main

import (
	"net/http"
	"sync"
	"time"
)

type schedulerConfig struct {
	InspectIntervalHours  float64
	ReenableIntervalHours float64
	ManagementKey         string
}

type autoScheduler struct {
	mu      sync.Mutex
	cfg     schedulerConfig
	stopCh  chan struct{}
	running bool
}

var scheduler = &autoScheduler{}

func (s *autoScheduler) reconfigure(cfg schedulerConfig) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

func (s *autoScheduler) start() {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.stopCh = make(chan struct{})
	s.running = true
	s.mu.Unlock()
	go s.loop()
}

func (s *autoScheduler) stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	close(s.stopCh)
	s.running = false
	s.mu.Unlock()
}

func (s *autoScheduler) loop() {
	// Check every minute, fire based on interval config
	inspectTicker := time.NewTicker(1 * time.Minute)
	defer inspectTicker.Stop()

	var lastInspect time.Time
	var lastReenable time.Time

	for {
		select {
		case <-s.stopCh:
			return
		case now := <-inspectTicker.C:
			s.mu.Lock()
			cfg := s.cfg
			s.mu.Unlock()

			// Auto re-enable disabled accounts
			if cfg.ReenableIntervalHours > 0 && cfg.ManagementKey != "" {
				interval := time.Duration(cfg.ReenableIntervalHours * float64(time.Hour))
				if lastReenable.IsZero() || now.Sub(lastReenable) >= interval {
					lastReenable = now
					go s.doReenable(cfg.ManagementKey)
				}
			}

			// Auto inspect + apply
			if cfg.InspectIntervalHours > 0 {
				interval := time.Duration(cfg.InspectIntervalHours * float64(time.Hour))
				if lastInspect.IsZero() || now.Sub(lastInspect) >= interval {
					lastInspect = now
					go s.doInspectAndApply(cfg.ManagementKey)
				}
			}
		}
	}
}

func (s *autoScheduler) doReenable(managementKey string) {
	// Re-enable all disabled xAI accounts via management API
	headers := make(http.Header)
	if managementKey != "" {
		headers.Set("X-Management-Key", managementKey)
	}
	engine.reenableDisabled(headers)
}

func (s *autoScheduler) doInspectAndApply(managementKey string) {
	// Only start if not already running
	if err := engine.start(startRequest{Incremental: false}); err != nil {
		return // already running, skip this tick
	}
	// Wait for inspection to complete (poll with timeout)
	timeout := time.After(30 * time.Minute)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-timeout:
			engine.stop()
			return
		case <-ticker.C:
			snap := engine.snapshot(false)
			if !snap.Running {
				// Inspection done — apply recommended actions
				if managementKey != "" {
					headers := make(http.Header)
					headers.Set("X-Management-Key", managementKey)
					_ = engine.startApply(applyRequest{}, managementKey, headers)
				}
				return
			}
		}
	}
}
