// Package trackstatus provides functionality for determining when a track has
// changed in a mixing situation.
package trackstatus

import (
	"sync"
	"time"

	"go.evanpurkhiser.com/prolink"
	"go.evanpurkhiser.com/prolink/bpm"
)

// An Event is a string key for status change events
type Event string

// Event constants
const (
	SetStarted Event = "set_started"
	SetEnded   Event = "set_ended"

	NowPlaying Event = "now_playing"
	Stopped    Event = "stopped"
	ComingSoon Event = "coming_soon"
)

// These are states where the track is passively playing
var playingStates = map[prolink.PlayState]bool{
	prolink.PlayStateLooping: true,
	prolink.PlayStatePlaying: true,
}

// These are the states where the track has been stopped
var stoppingStates = map[prolink.PlayState]bool{
	prolink.PlayStateEnded:   true,
	prolink.PlayStateCued:    true,
	prolink.PlayStateLoading: true,
}

// HandlerFunc is a function that will be called when the player track
// is considered to be changed. This includes the CDJStatus object that
// triggered the change.
type HandlerFunc func(Event, *prolink.CDJStatus)

// Config specifies configuration for the Handler.
type Config struct {
	// AllowedInterruptBeats configures how many beats a track may not be live
	// or playing for it to still be considered active.
	AllowedInterruptBeats int

	// BeatsUntilReported configures how many beats the track must consecutively
	// be playing for (since the beat it was cued at) until the track is
	// considered to be active.
	BeatsUntilReported int

	// TimeBetweenSets specifies the duration that no tracks must be on air.
	// This can be thought of as how long 'air silence' is reasonble in a set
	// before a separate one has begun.
	TimeBetweenSets time.Duration
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
// The following track statuses are reported:
//
// - NowPlaying: The track is considered playing and on air to the audiance.
// - Stopped:    The track was stopped.
// - ComingSoon: A new track has been loaded.
//
// Additionally the following non-track status are reported:
//
// - SetStarted: The first track has begun playing.
// - SetEnded:   The TimeBetweenSets has passed since any tracks were live.
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
// - A incoming track will immediately be reported as NowPlaying if it is on
//   air, playing, and the last active track has been cued.
//
// - A incoming track will be reported as NowPlaying if the active track has
//   not been on air or has not been playing for the configured
//   AllowedInterruptBeats.
//
// - A incoming track will be reported as NowPlaying if it has played
//   consecutively (with AllowedInterruptBeats honored for the incoming track)
//   for the configured BeatsUntilReported.
//
// - A track will be reported as Stopped when it was NowPlaying and was stopped
//   (cued, reached the end of the track, or a new track was loaded.
//
// - A track will be reported as ComingSoon when a new track is selected.
type Handler struct {
	config  Config
	handler HandlerFunc

	lock            sync.Mutex
	lastStatus      map[prolink.DeviceID]*prolink.CDJStatus
	lastStartTime   map[prolink.DeviceID]time.Time
	interruptCancel map[prolink.DeviceID]chan bool
	wasReportedLive map[prolink.DeviceID]bool

	setInProgress   bool
	setEndingCancel chan bool
}

// reportPlayer triggers the track change handler if track on the given device
// has not already been reported live and is currently on air.
func (h *Handler) reportPlayer(pid prolink.DeviceID) {
	// Track has already been reported
	if h.wasReportedLive[pid] {
		return
	}

	if !h.lastStatus[pid].IsOnAir {
		return
	}

	h.wasReportedLive[pid] = true

	if !h.setInProgress {
		h.setInProgress = true
		h.handler(SetStarted, h.lastStatus[pid])
	}

	if h.setEndingCancel != nil {
		h.setEndingCancel <- true
	}

	h.handler(NowPlaying, h.lastStatus[pid])
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

	if earliestPID == 0 {
		return
	}

	h.reportPlayer(earliestPID)
}

// setMayEnd signals that we should wait the specified timeout period for no
// tracks to become onair and playing to mark a set as having "ended".
func (h *Handler) setMayEnd() {
	if !h.setInProgress {
		return
	}

	// set may already be ending. Do not start a new waiter
	if h.setEndingCancel != nil {
		return
	}

	// Ensure all players are stopped
	for _, s := range h.lastStatus {
		if playingStates[s.PlayState] {
			return
		}
	}

	h.setEndingCancel = make(chan bool)

	timer := time.NewTimer(h.config.TimeBetweenSets)

	select {
	case <-h.setEndingCancel:
		break
	case <-timer.C:
		h.handler(SetEnded, &prolink.CDJStatus{})
		h.setInProgress = false
		break
	}

	h.setEndingCancel = nil
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
	beatDuration := bpm.ToDuration(s.TrackBPM, s.SliderPitch)
	timeout := beatDuration * time.Duration(h.config.AllowedInterruptBeats)

	timer := time.NewTimer(timeout)

	select {
	case <-h.interruptCancel[s.PlayerID]:
		break
	case <-timer.C:
		delete(h.lastStartTime, s.PlayerID)
		h.handler(Stopped, s)
		h.wasReportedLive[s.PlayerID] = false

		h.reportNextPlayer()
		h.setMayEnd()
		break
	}

	delete(h.interruptCancel, s.PlayerID)
}

// trackMayBeFirst checks that no other tracks are currently on air and
// playing, other than the current one who's status is being reported as
// playing, and will report it as live if this is true.
func (h *Handler) trackMayBeFirst(s *prolink.CDJStatus) {
	for _, otherStatus := range h.lastStatus {
		if otherStatus.PlayerID == s.PlayerID {
			continue
		}

		// Another device is already on air and playing. This is not the first
		if otherStatus.IsOnAir && playingStates[otherStatus.PlayState] {
			return
		}
	}

	h.reportPlayer(s.PlayerID)
}

// playStateChange updates the lastPlayTime of the track on the player who's
// status is being reported.
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
			h.trackMayBeFirst(s)
		} else {
			cancelInterupt <- true
		}

		return
	}

	// Track was stopped. Immediately promote another track to be reported and
	// report the track as being stopped.
	if wasPlaying && h.wasReportedLive[pid] && stoppingStates[s.PlayState] {
		if cancelInterupt, ok := h.interruptCancel[pid]; ok {
			cancelInterupt <- true
		}

		delete(h.lastStartTime, pid)
		h.reportNextPlayer()

		h.handler(Stopped, s)
		h.wasReportedLive[s.PlayerID] = false
		go h.setMayEnd()

		return
	}

	if wasPlaying && !nowPlaying && h.wasReportedLive[pid] {
		go h.trackMayStop(s)
	}
}

// OnStatusUpdate implements the prolink.StatusHandler interface
func (h *Handler) OnStatusUpdate(s *prolink.CDJStatus) {
	h.lock.Lock()
	defer h.lock.Unlock()

	pid := s.PlayerID
	ls, ok := h.lastStatus[pid]

	h.lastStatus[pid] = s

	// If this is the first we've heard from this CDJ and it's on air and
	// playing immediately report it
	if !ok && s.IsOnAir && playingStates[s.PlayState] {
		h.lastStartTime[pid] = time.Now()
		h.reportPlayer(s.PlayerID)

		return
	}

	// Populate last play state with an empty status packet to initialize
	if !ok {
		ls = &prolink.CDJStatus{}
	}

	// Play state has changed
	if ls.PlayState != s.PlayState {
		h.playStateChange(ls, s)
	}

	// On-Air state has changed
	if ls.IsOnAir != s.IsOnAir {
		if !s.IsOnAir {
			go h.trackMayStop(s)
		}

		if s.IsOnAir && h.interruptCancel[pid] != nil {
			h.interruptCancel[pid] <- true
		}
	}

	// Only report a track as coming soon if at least one other track is
	// currently playing.
	shouldReportComingSoon := false

	for _, reportedLive := range h.wasReportedLive {
		if reportedLive {
			shouldReportComingSoon = true
			break
		}
	}

	// New track loaded. Reset reported-live flag and report ComingSoon
	if ls.TrackID != s.TrackID && shouldReportComingSoon {
		h.wasReportedLive[pid] = false
		h.handler(ComingSoon, s)
	}

	// If the track on this deck has been playing for more than the configured
	// BeatsUntilReported (as calculated given the current BPM) report it
	beatDuration := bpm.ToDuration(s.TrackBPM, s.SliderPitch)
	timeTillReport := beatDuration * time.Duration(h.config.BeatsUntilReported)

	lst, ok := h.lastStartTime[pid]

	if ok && lst.Add(timeTillReport).Before(time.Now()) {
		h.reportPlayer(pid)
	}
}
