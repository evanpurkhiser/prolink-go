// Package mixstatus provides functionality for determining when a track has
// changed in a mixing situation.
package mixstatus

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

// Handler is a interface that may be implemented to receive mix status events
// This includes the CDJStatus object that triggered the change.
type Handler interface {
	OnMixStatus(Event, *prolink.CDJStatus)
}

// HandlerFunc is an adapter to implement the handler.
type HandlerFunc func(Event, *prolink.CDJStatus)

// OnMixStatus implements the Handler interface.
func (f HandlerFunc) OnMixStatus(e Event, s *prolink.CDJStatus) { f(e, s) }

func noopHandlerFunc(e Event, s *prolink.CDJStatus) {}

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
	// This can be thought of as how long 'air silence' is reasonable in a set
	// before a separate one has begun.
	TimeBetweenSets time.Duration
}

// NewProcessor constructs a new Processor to watch for track changes
func NewProcessor(config Config, handler Handler) *Processor {
	if handler == nil {
		handler = HandlerFunc(noopHandlerFunc)
	}

	processor := Processor{
		Config:          config,
		handler:         handler.OnMixStatus,
		lock:            sync.Mutex{},
		lastStatus:      map[prolink.DeviceID]*prolink.CDJStatus{},
		lastStartTime:   map[prolink.DeviceID]time.Time{},
		interruptCancel: map[prolink.DeviceID]chan bool{},
		wasReportedLive: map[prolink.DeviceID]bool{},
	}

	return &processor
}

// Processor is a configurable object which implements the
// prolink.StatusListener interface to more accurately detect when a track has
// changed in a mixing situation.
//
// The following track statuses are reported:
//
// - NowPlaying: The track is considered playing and on air to the audience.
// - Stopped:    The track was stopped / paused.
// - ComingSoon: A new track has been loaded.
//
// Additionally the following non-track status are reported:
//
// - SetStarted: The first track has begun playing.
// - SetEnded:   The TimeBetweenSets has passed since any tracks were live.
//
// See Config for configuration options.
//
// Config options may be changed after the processor has been constructed and
// is actively receiving status updates.
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
type Processor struct {
	Config  Config
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
func (p *Processor) reportPlayer(pid prolink.DeviceID) {
	// Track has already been reported
	if p.wasReportedLive[pid] {
		return
	}

	if !p.lastStatus[pid].IsOnAir {
		return
	}

	p.wasReportedLive[pid] = true

	if !p.setInProgress {
		p.setInProgress = true
		p.handler(SetStarted, p.lastStatus[pid])
	}

	if p.setEndingCancel != nil {
		p.setEndingCancel <- true
	}

	p.handler(NowPlaying, p.lastStatus[pid])
}

// reportNextPlayer finds the longest playing track that has not been reported
// live and reports it as live.
func (p *Processor) reportNextPlayer() {
	var earliestPID prolink.DeviceID
	earliestTime := time.Now()

	// Locate the player that's been playing for the longest
	for pid, lastStartTime := range p.lastStartTime {
		isEarlier := lastStartTime.Before(earliestTime)

		if isEarlier && !p.wasReportedLive[pid] {
			earliestTime = lastStartTime
			earliestPID = pid
		}
	}

	if earliestPID == 0 {
		return
	}

	p.reportPlayer(earliestPID)
}

// setMayEnd signals that we should wait the specified timeout period for no
// tracks to become onair and playing to mark a set as having "ended".
func (p *Processor) setMayEnd() {
	if !p.setInProgress {
		return
	}

	// set may already be ending. Do not start a new waiter
	if p.setEndingCancel != nil {
		return
	}

	// Ensure all players are stopped
	for _, s := range p.lastStatus {
		if playingStates[s.PlayState] {
			return
		}
	}

	p.setEndingCancel = make(chan bool)

	timer := time.NewTimer(p.Config.TimeBetweenSets)

	select {
	case <-p.setEndingCancel:
		break
	case <-timer.C:
		p.handler(SetEnded, &prolink.CDJStatus{})
		p.setInProgress = false
		break
	}

	p.setEndingCancel = nil
}

// trackMayStop tracks that a track may be stopping. Wait the configured
// interrupt beat interval and report the next track as live if it has stopped.
// May be canceled if the track comes back on air.
func (p *Processor) trackMayStop(s *prolink.CDJStatus) {
	// track already may stop. Do not start a new waiter.
	if _, ok := p.interruptCancel[s.PlayerID]; ok {
		return
	}

	p.interruptCancel[s.PlayerID] = make(chan bool)

	// Wait for the AllowedInterruptBeats based off the current BPM
	beatDuration := bpm.ToDuration(s.TrackBPM, s.SliderPitch)
	timeout := beatDuration * time.Duration(p.Config.AllowedInterruptBeats)

	timer := time.NewTimer(timeout)

	select {
	case <-p.interruptCancel[s.PlayerID]:
		break
	case <-timer.C:
		delete(p.lastStartTime, s.PlayerID)
		p.handler(Stopped, s)
		p.wasReportedLive[s.PlayerID] = false

		p.reportNextPlayer()
		p.setMayEnd()
		break
	}

	delete(p.interruptCancel, s.PlayerID)
}

// trackMayBeFirst checks that no other tracks are currently on air and
// playing, other than the current one who's status is being reported as
// playing, and will report it as live if this is true.
func (p *Processor) trackMayBeFirst(s *prolink.CDJStatus) {
	for _, otherStatus := range p.lastStatus {
		if otherStatus.PlayerID == s.PlayerID {
			continue
		}

		// Another device is already on air and playing. This is not the first
		if otherStatus.IsOnAir && playingStates[otherStatus.PlayState] {
			return
		}
	}

	p.reportPlayer(s.PlayerID)
}

// playStateChange updates the lastPlayTime of the track on the player who's
// status is being reported.
func (p *Processor) playStateChange(lastState, s *prolink.CDJStatus) {
	pid := s.PlayerID

	nowPlaying := playingStates[s.PlayState]
	wasPlaying := playingStates[lastState.PlayState]

	// Track has begun playing. Mark the start time or cancel interrupt
	// timers from when the track was previously stopped.
	if !wasPlaying && nowPlaying {
		cancelInterupt := p.interruptCancel[pid]

		if cancelInterupt == nil {
			p.lastStartTime[pid] = time.Now()
			p.trackMayBeFirst(s)
		} else {
			cancelInterupt <- true
		}

		return
	}

	// Track was stopped. Immediately promote another track to be reported and
	// report the track as being stopped.
	if wasPlaying && p.wasReportedLive[pid] && stoppingStates[s.PlayState] {
		if cancelInterupt, ok := p.interruptCancel[pid]; ok {
			cancelInterupt <- true
		}

		if playingStates[p.lastStatus[s.PlayerID].PlayState] {
			return
		}

		delete(p.lastStartTime, pid)
		p.reportNextPlayer()

		p.handler(Stopped, s)
		p.wasReportedLive[s.PlayerID] = false
		go p.setMayEnd()

		return
	}

	if wasPlaying && !nowPlaying && p.wasReportedLive[pid] {
		go p.trackMayStop(s)
	}
}

// OnStatusUpdate implements the prolink.StatusHandler interface
func (p *Processor) OnStatusUpdate(s *prolink.CDJStatus) {
	p.lock.Lock()
	defer p.lock.Unlock()

	pid := s.PlayerID
	ls, ok := p.lastStatus[pid]

	p.lastStatus[pid] = s

	// If this is the first we've heard from this CDJ and it's on air and
	// playing immediately report it
	if !ok && s.IsOnAir && playingStates[s.PlayState] {
		p.lastStartTime[pid] = time.Now()
		p.reportPlayer(s.PlayerID)

		return
	}

	// Populate last play state with an empty status packet to initialize
	if !ok {
		ls = &prolink.CDJStatus{}
	}

	// Play state has changed
	if ls.PlayState != s.PlayState {
		p.playStateChange(ls, s)
	}

	// On-Air state has changed
	if ls.IsOnAir != s.IsOnAir {
		if !s.IsOnAir && playingStates[s.PlayState] {
			go p.trackMayStop(s)
		}

		if s.IsOnAir && p.interruptCancel[pid] != nil {
			p.interruptCancel[pid] <- true
		}
	}

	// Only report a track as coming soon if at least one other track is
	// currently playing.
	shouldReportComingSoon := false

	for _, reportedLive := range p.wasReportedLive {
		if reportedLive {
			shouldReportComingSoon = true
			break
		}
	}

	// New track loaded. Reset reported-live flag and report ComingSoon
	if ls.TrackID != s.TrackID && shouldReportComingSoon {
		p.wasReportedLive[pid] = false
		p.handler(ComingSoon, s)
	}

	// If the track on this deck has been playing for more than the configured
	// BeatsUntilReported (as calculated given the current BPM) report it
	beatDuration := bpm.ToDuration(s.TrackBPM, s.SliderPitch)
	timeTillReport := beatDuration * time.Duration(p.Config.BeatsUntilReported)

	lst, ok := p.lastStartTime[pid]

	if ok && lst.Add(timeTillReport).Before(time.Now()) {
		p.reportPlayer(pid)
	}
}

func (p *Processor) SetHandler(handler Handler) {
	p.handler = handler.OnMixStatus
}
