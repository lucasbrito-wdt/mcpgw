package main

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const rssSampleEvery = 500 * time.Millisecond

type rssStats struct {
	startMB, peakMB, endMB float64
	samples                int
}

// rssSampler polls `ps -o rss=` for one process. A zero pid disables it.
type rssSampler struct {
	pid   int
	quit  chan struct{}
	done  sync.WaitGroup
	stats rssStats
}

func startRSSSampler(pid int) *rssSampler {
	s := &rssSampler{pid: pid, quit: make(chan struct{})}
	if pid <= 0 {
		return s
	}
	s.sample()
	s.done.Add(1)
	go func() {
		defer s.done.Done()
		tick := time.NewTicker(rssSampleEvery)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				s.sample()
			case <-s.quit:
				return
			}
		}
	}()
	return s
}

func (s *rssSampler) sample() {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(s.pid)).Output()
	if err != nil {
		return
	}
	kb, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return
	}
	mb := kb / 1024
	if s.stats.samples == 0 {
		s.stats.startMB = mb
	}
	s.stats.peakMB = max(s.stats.peakMB, mb)
	s.stats.endMB = mb
	s.stats.samples++
}

func (s *rssSampler) stop() rssStats {
	if s.pid <= 0 {
		return rssStats{}
	}
	close(s.quit)
	s.done.Wait()
	s.sample()
	return s.stats
}
