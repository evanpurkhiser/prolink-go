// Package trackchange provides functionality for determining when a track has
// changed in a mixing situation.
package trackchange

import (
	"sync"
	"time"

	"go.evanpurkhiser.com/prolink"
)

// These are states where the track is passively playing
var playingStates = map[prolink.PlayState]bool{
	prolink.PlayStateLooping: true,
	prolink.PlayStatePlaying: true,
}

// HandlerFunc is a function that will be called when the player track
// is considered to be changed.
type HandlerFunc func(prolink.DeviceID, uint32)

// Config specifies configuration for the Handler.
type Config struct {
	// AllowedInterruptBeats configures how many beats a track may not be live
	// or playing for it to still be considered active.
	AllowedInterruptBeats int

	// BeatsUntilReported configures how many beats the track must consecutively
	// be playing for (since the beat it was cued at) until the track is
	// considered to be active.
	BeatsUntilReported int
}

// bpmToDuration converts bpm and pitch information into a one beat duration.
func bpmToDuration(bpm, pitch float32) time.Duration {
	bps := ((pitch / 100 * bpm) + bpm) / 60

	return time.Duration(float32(time.Second) / bps)
}

// NewHandler constructs a new Handler to watch for track changes
func NewHandler(config Config, fn HandlerFunc) *Handler {
	handler := Handler{
		config:          config,
		handler:         fn,
		lock:            sync.Mutex{},
		lastStatus:      map[prolink.DeviceID]*prolink.CDJStatus{},
		lastStartTime:   map[prolink.DeviceID]time.Time{},
		interruptCancel: map[prolink.DeviceID]chan bool{},
		wasReportedLive: map[prolink.DeviceID]bool{},
	}

	return &handler
}

// Handler is a configurable object which implements the prolink.StatusListener
// interface to more accurately detect when a track has changed in a mixing
// situation.
//
// See Config for configuration options.
//
// Track changes are detected based on a number of rules:
//
// - The track that has been in the play state with the CDJ in the "on air" state
//   for the longest period of time (allowing for a configurable length of
//   interruption with AllowedInterruptBeats) is considered to be the active
//   track that incoming tracks will be compared against.
//
// - A incoming track will immediately be reported if it is on air, playing, and
//   the last active track has been cued.
//
// - A incoming track will be repotred if the active track has not been on air
//   or has not been playing for the configured AllowedInterruptBeats.
//
// - A incoming track will be reported if it has played consecutively (with
//   AllowedInterruptBeats honored for the incoming track) for the configured
//   BeatsUntilReported.
type Handler struct {
	config  Config
	handler HandlerFunc

	lock            sync.Mutex
	lastStatus      map[prolink.DeviceID]*prolink.CDJStatus
	lastStartTime   map[prolink.DeviceID]time.Time
	interruptCancel map[prolink.DeviceID]chan bool
	wasReportedLive map[prolink.DeviceID]bool
}

// reportPlayer triggers the track change handler if track on the given device
// has not already been reported live and is currently live.
func (h *Handler) reportPlayer(pid prolink.DeviceID) {
	// Track has already been reported
	if h.wasReportedLive[pid] {
		return
	}

	if !h.lastStatus[pid].IsLive {
		return
	}

	h.wasReportedLive[pid] = true

	h.handler(pid, h.lastStatus[pid].TrackID)
}

// reportNextPlayer finds the longest playing track that has not been reported
// live and reports it as live.
func (h *Handler) reportNextPlayer() {
	var earliestPID prolink.DeviceID
	earliestTime := time.Now()

	// Locate the player that's been playing for the longest
	for pid, lastStartTime := range h.lastStartTime {
		isEarlier := lastStartTime.Before(earliestTime)

		if isEarlier && !h.wasReportedLive[pid] {
			earliestTime = lastStartTime
			earliestPID = pid
		}
	}

	// No other tracks are currently playing
	if earliestPID == 0 {
		return
	}

	h.reportPlayer(earliestPID)
}

// trackMayStop tracks that a track may be stopping. Wait the configured
// interrupt beat interval and report the next track as live if it has stopped.
// May be canceld if the track comes back on air.
func (h *Handler) trackMayStop(s *prolink.CDJStatus) {
	// track already may stop. Do not start a new waiter.
	if _, ok := h.interruptCancel[s.PlayerID]; ok {
		return
	}

	h.interruptCancel[s.PlayerID] = make(chan bool)

	// Wait for the AllowedInterruptBeats based off the current BPM
	beatDuration := bpmToDuration(s.TrackBPM, s.SliderPitch)
	timeout := beatDuration * time.Duration(h.config.AllowedInterruptBeats)

	timer := time.NewTimer(timeout)

	select {
	case <-h.interruptCancel[s.PlayerID]:
		break
	case <-timer.C:
		delete(h.lastStartTime, s.PlayerID)
		h.reportNextPlayer()
		break
	}

	delete(h.interruptCancel, s.PlayerID)
}

// playStateChange updates the lastPlayTime of the track on the player whos
// status is being reported. This will
func (h *Handler) playStateChange(lastState, s *prolink.CDJStatus) {
	pid := s.PlayerID

	nowPlaying := playingStates[s.PlayState]
	wasPlaying := playingStates[lastState.PlayState]

	// Track has begun playing. Mark the start time or cancel interrupt
	// timers from when the track was previously stopped.
	if !wasPlaying && nowPlaying {
		cancelInterupt := h.interruptCancel[pid]

		if cancelInterupt == nil {
			h.lastStartTime[pid] = time.Now()
		} else {
			cancelInterupt <- true
		}

		return
	}

	// Track was cued. Immediately promote another track to be reported
	if wasPlaying && s.PlayState == prolink.PlayStateCued {
		if cancelInterupt, ok := h.interruptCancel[pid]; ok {
			cancelInterupt <- true
		}

		delete(h.lastStartTime, s.PlayerID)
		h.reportNextPlayer()

		return
	}

	if wasPlaying && !nowPlaying {
		go h.trackMayStop(s)
	}
}

// OnStatusUpdate implements the prolink.StatusHandler interface
func (h *Handler) OnStatusUpdate(s *prolink.CDJStatus) {
	h.lock.Lock()
	defer h.lock.Unlock()

	pid := s.PlayerID
	ls, ok := h.lastStatus[pid]

	// Populate last play state with an empty status packet to initialze
	if !ok {
		ls = &prolink.CDJStatus{}
	}

	// Play state has changed
	if ls.PlayState != s.PlayState {
		h.playStateChange(ls, s)
	}

	// On-Air (live) state has changed
	if ls.IsLive != s.IsLive {
		if !s.IsLive {
			go h.trackMayStop(s)
		}

		if s.IsLive && h.interruptCancel[pid] != nil {
			h.interruptCancel[pid] <- true
		}
	}

	// New track loaded. Reset reported-live flag
	if ls.TrackID != s.TrackID {
		h.wasReportedLive[pid] = false
	}

	// If the track on this deck has been playing for more than the configured
	// BeatsUntilReported (as calculated given the current BPM) report it
	beatDuration := bpmToDuration(s.TrackBPM, s.SliderPitch)
	timeTillReport := beatDuration * time.Duration(h.config.BeatsUntilReported)

	lst, ok := h.lastStartTime[pid]

	if ok && lst.Add(timeTillReport).Before(time.Now()) {
		h.reportPlayer(pid)
	}

	h.lastStatus[pid] = s
}
