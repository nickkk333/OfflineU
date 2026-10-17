// Package offlineu implements the self-hosted, offline course viewer and
// progress tracker that powers the OfflineU web application.
//
// This file keeps track of a cast that is running somewhere else: the picture
// is on the TV, but the browser still shows where the lesson is, writes the
// position into the progress file and - when the lesson ends - pushes the next
// one to the same device without anyone touching a remote.
package offlineu

import (
	"context"
	"errors"
	"sync"
	"time"
)

// CastState is what the device is doing from OfflineU's point of view.
type CastState string

const (
	CastStatePlaying CastState = "playing"
	CastStatePaused  CastState = "paused"
	CastStateEnded   CastState = "ended"
)

// Tuning of the cast watchdog.
const (
	castPollInterval   = 3 * time.Second  // how often the device is asked where it is
	castSaveInterval   = 15.0             // seconds between two progress writes
	castEndTolerance   = 2.0              // seconds before the end that count as "finished"
	castPositionFresh  = 12 * time.Second // a reported position older than this is stale
	castPositionMaxAge = 6 * time.Hour    // a cast nobody stopped is dropped eventually
	// Right after a hand-over the device may still describe the lesson it was
	// told to leave; a stop it reports inside this window is not this lesson's.
	castHandoverGrace = 5 * time.Second
	// The watchdog's ffprobe budget for the next lesson. No browser is waiting
	// on it, and without a length the cast can never see the lesson end - so a
	// cold disk gets considerably more time than a request path would.
	castProbeTimeout = 20 * time.Second
	// A position that jumps back to the beginning counts as the device having
	// replayed the lesson (its end, whether or not the length is known): it
	// takes this many played seconds before such a jump means anything, and
	// the beginning is anything within this many seconds of zero.
	castLoopRestartMin = 30.0
	castLoopRestartMax = 10.0
)

// CastSession is one lesson playing on one renderer.
type CastSession struct {
	UDN         string
	Device      string
	CoursePath  string
	LessonPath  string // course relative path (progress key)
	LessonURL   string // lesson URL, used by the SPA route
	LessonTitle string
	BaseURL     string // scheme + host, so the watchdog can build URLs too
	Converted   bool
	MediaFile   string // absolute path of the file being played

	StartSeconds float64   // resume position the cast started from
	Duration     float64   // media duration in seconds, 0 when unknown
	StartedAt    time.Time // when the last lesson was handed to the device
	PausedAt     time.Time // zero while running
	PausedFor    float64   // seconds spent paused
	State        CastState

	RealPosition float64   // last position the device reported
	RealAt       time.Time // when it reported it
	RealState    string    // TransportState of the device
	LastSaved    float64   // last position written to the progress file
	LastAsked    time.Time // last GetPositionInfo call
	stopped      bool
	watching     bool // a watchdog goroutine is looking after this session

	// DeviceLooped marks that the renderer started the current media over by
	// itself: the lesson therefore ended, whether or not its length is known.
	DeviceLooped bool
	// sawPlaying is set once the device confirmed this lesson with a PLAYING
	// report. A device that never started (it refused the media, say) must not
	// be able to end it by sitting in STOPPED - the cast would walk through
	// the whole course marking lessons completed that never played.
	sawPlaying bool
}

// elapsed is how long the device has been playing this lesson.
func (s *CastSession) elapsed(now time.Time) float64 {
	seconds := now.Sub(s.StartedAt).Seconds() - s.PausedFor
	if !s.PausedAt.IsZero() {
		seconds -= now.Sub(s.PausedAt).Seconds()
	}
	if seconds < 0 {
		return 0
	}
	return seconds
}

// estimate is the position as far as OfflineU can work it out on its own.
func (s *CastSession) estimate(now time.Time) float64 {
	position := s.StartSeconds + s.elapsed(now)
	if s.Duration > 0 && position > s.Duration {
		position = s.Duration
	}
	return position
}

// position answers where the lesson is: the device knows best, everything else
// is clock arithmetic. A report older than castPositionFresh is ignored, so a
// device that went silent does not freeze the progress bar forever.
func (s *CastSession) position(now time.Time) (float64, bool) {
	if s.State == CastStateEnded {
		return s.Duration, false
	}
	if !s.RealAt.IsZero() && now.Sub(s.RealAt) < castPositionFresh && s.RealPosition > 0 {
		position := s.RealPosition
		// Only keep counting while the device says it is playing.
		if s.RealState == "PLAYING" {
			position += now.Sub(s.RealAt).Seconds()
		}
		if s.Duration > 0 && position > s.Duration {
			position = s.Duration
		}
		return position, true
	}
	return s.estimate(now), false
}

// finished reports whether the lesson has run to its end. The position only
// tells that when the length is known - the other two signals cover the rest:
// a device that stopped is done, and a device that started the media over has
// seen the lesson end too (it just does not say so). Without them a lesson of
// unknown length would loop on the device forever, because nothing could ever
// hand the next one over.
func (s *CastSession) finished(now time.Time) bool {
	if s.State == CastStateEnded {
		return false
	}
	if s.DeviceLooped {
		return true
	}
	if s.deviceStopped(now) {
		return true
	}
	if s.Duration <= 0 {
		return false
	}
	position, _ := s.position(now)
	return position >= s.Duration-castEndTolerance
}

// deviceStopped reports whether the device itself said STOPPED recently. A stop
// right after the hand-over still belongs to the lesson the device was just
// told to leave, so the first seconds of a new lesson are excluded - and a
// device that never confirmed PLAYING for this lesson cannot end it by sitting
// still, otherwise a refused file would march the cast through the course.
func (s *CastSession) deviceStopped(now time.Time) bool {
	if s.RealState != "STOPPED" || s.RealAt.IsZero() || now.Sub(s.RealAt) >= castPositionFresh {
		return false
	}
	if !s.sawPlaying {
		return false
	}
	return now.Sub(s.StartedAt) > castHandoverGrace
}

// deviceLooped reports whether the newest position tells of a restart: the
// device played well into a lesson whose length is unknown and is now back at
// the beginning - it ran the media to its end and started it over instead of
// stopping. Only a restart while playing counts, and only against a fresh
// previous report; a rewind done through the cast bar goes through setPosition,
// which clears the position this compares against.
func (s *CastSession) deviceLooped(info PositionInfo, now time.Time) bool {
	if s.Duration > 0 {
		return false // the position check sees the end without help
	}
	if s.RealAt.IsZero() || now.Sub(s.RealAt) >= castPositionFresh {
		return false // nothing fresh to compare against
	}
	if s.RealState != "PLAYING" || info.TransportState != "PLAYING" {
		return false // only a restart while playing counts
	}
	if s.RealPosition < castLoopRestartMin {
		return false // it never got far enough for this to mean a restart
	}
	return info.RelTime <= castLoopRestartMax && info.RelTime < s.RealPosition
}

// pause freezes the clock until resume is called.
func (s *CastSession) pause(now time.Time) {
	if s.PausedAt.IsZero() {
		s.PausedAt = now
	}
}

// resume lets the clock run again.
func (s *CastSession) resume(now time.Time) {
	if !s.PausedAt.IsZero() {
		s.PausedFor += now.Sub(s.PausedAt).Seconds()
		s.PausedAt = time.Time{}
	}
}

// setPosition moves a running cast to a position, forgetting everything the
// clock and the device knew about the old one.
func (s *CastSession) setPosition(position float64, now time.Time, paused bool) {
	s.StartSeconds = position
	s.StartedAt = now
	s.PausedFor = 0
	s.PausedAt = time.Time{}
	s.RealPosition = 0
	s.RealAt = time.Time{}
	s.RealState = ""
	s.LastSaved = position
	s.State = CastStatePlaying
	s.DeviceLooped = false
	s.sawPlaying = false
	if paused {
		s.PausedAt = now
	}
}

// reset points the session at the next lesson it is about to play.
func (s *CastSession) reset(lesson *Lesson, file string, duration float64, converted bool, now time.Time) {
	s.LessonPath = lesson.RelPath
	s.LessonURL = lesson.URL
	s.LessonTitle = lesson.Title
	s.MediaFile = file
	s.Duration = duration
	s.Converted = converted
	s.StartSeconds = 0
	s.StartedAt = now
	s.PausedAt = time.Time{}
	s.PausedFor = 0
	s.RealPosition = 0
	s.RealAt = time.Time{}
	s.RealState = ""
	s.LastSaved = 0
	s.State = CastStatePlaying
	s.DeviceLooped = false
	s.sawPlaying = false
}

// CastSessionView is the JSON the browser polls: enough to draw a progress bar
// and to know which lesson the TV is on right now.
type CastSessionView struct {
	Active         bool    `json:"active"`
	UDN            string  `json:"udn,omitempty"`
	Device         string  `json:"device,omitempty"`
	LessonPath     string  `json:"lesson_path,omitempty"`
	LessonURL      string  `json:"lesson_url,omitempty"`
	LessonTitle    string  `json:"lesson_title,omitempty"`
	CoursePath     string  `json:"course_path,omitempty"`
	Position       float64 `json:"position"`
	Duration       float64 `json:"duration"`
	State          string  `json:"state"`
	Converted      bool    `json:"converted"`
	Reported       bool    `json:"reported"`           // the device told us, not the clock
	Subtitle       bool    `json:"subtitle,omitempty"` // the cast carries captions
	NextTitle      string  `json:"next_title,omitempty"`
	NextLessonPath string  `json:"next_lesson_path,omitempty"`
}

// castLock guards the single active cast session of this process.
type castLock struct {
	mutex   sync.Mutex
	session *CastSession
}

// begin replaces the running cast (if any) and starts watching the new one.
func (a *App) beginCast(session *CastSession) {
	a.cast.mutex.Lock()
	if previous := a.cast.session; previous != nil {
		previous.stopped = true
	}
	a.cast.session = session
	session.watching = true
	a.cast.mutex.Unlock()
	go a.watchCast(session)
}

// currentCast returns the session that is still this process's active cast.
func (a *App) currentCast() *CastSession {
	a.cast.mutex.Lock()
	defer a.cast.mutex.Unlock()
	return a.cast.session
}

// endCast drops the session, which stops its watchdog.
func (a *App) endCast(session *CastSession) {
	a.cast.mutex.Lock()
	defer a.cast.mutex.Unlock()
	if session != nil {
		session.stopped = true
	}
	if a.cast.session == session || session == nil {
		a.cast.session = nil
	}
}

// castSnapshot renders the active session for /api/dlna/session.
func (a *App) castSnapshot() CastSessionView {
	session := a.currentCast()
	if session == nil {
		return CastSessionView{Active: false}
	}
	a.cast.mutex.Lock()
	defer a.cast.mutex.Unlock()

	now := time.Now()
	position, reported := session.position(now)
	state := string(session.State)
	if session.State != CastStateEnded && !session.PausedAt.IsZero() {
		state = string(CastStatePaused)
	}
	view := CastSessionView{
		Active:      true,
		UDN:         session.UDN,
		Device:      session.Device,
		LessonPath:  session.LessonPath,
		LessonURL:   session.LessonURL,
		LessonTitle: session.LessonTitle,
		CoursePath:  session.CoursePath,
		Position:    position,
		Duration:    session.Duration,
		State:       state,
		Converted:   session.Converted,
		Reported:    reported,
	}
	if course := a.Store.Get(); course != nil && course.Path == session.CoursePath {
		if next := nextPlayableLesson(course, session.LessonPath); next != nil {
			view.NextTitle = next.Title
			view.NextLessonPath = next.RelPath
		}
		if lesson := FindLessonInTree(course.Root, session.LessonPath); lesson != nil {
			view.Subtitle = lesson.SubtitleFile != ""
		}
	}
	return view
}

// watchCast looks after one cast for as long as it is the active one: it asks
// the device where it is, writes the position into the progress file and hands
// the next lesson over when the current one ends.
func (a *App) watchCast(session *CastSession) {
	ticker := time.NewTicker(castPollInterval)
	defer ticker.Stop()
	for range ticker.C {
		if !a.castTick(session) {
			a.cast.mutex.Lock()
			session.watching = false
			a.cast.mutex.Unlock()
			return
		}
	}
}

// castTick is one watchdog round. It reports false when the session is over
// (stopped, replaced, or finished without a next lesson to play).
func (a *App) castTick(session *CastSession) bool {
	a.cast.mutex.Lock()
	if session == nil || session.stopped || a.cast.session != session {
		if session != nil {
			session.watching = false
		}
		a.cast.mutex.Unlock()
		return false
	}
	now := time.Now()
	if now.Sub(session.StartedAt) > castPositionMaxAge {
		session.stopped = true
		session.watching = false
		a.cast.mutex.Unlock()
		return false
	}
	course := a.Store.Get()
	if course == nil {
		session.watching = false
		a.cast.mutex.Unlock()
		return false
	}
	if course.Path != session.CoursePath {
		// Another course is open in the browser. The TV keeps playing, so the
		// cast is neither stopped nor progressed here - the watchdog simply
		// waits until that course is opened again (or the 6 h guard fires).
		a.cast.mutex.Unlock()
		return true
	}

	// Ask the device where it is - a remote control in the living room can
	// pause or skip without telling the browser anything.
	if now.Sub(session.LastAsked) >= castPollInterval {
		if renderer, ok := a.DLNA.Lookup(session.UDN); ok {
			session.LastAsked = now
			if info, err := a.DLNA.Position(renderer); err == nil {
				if info.RelTime > 0 || info.TrackDuration > 0 {
					// A device that plays a lesson of unknown length to its
					// end and simply starts it over never says STOPPED - the
					// jump back to the beginning is the only trace it leaves.
					if session.deviceLooped(info, now) {
						session.DeviceLooped = true
					}
					session.RealPosition = info.RelTime
					session.RealAt = now
					session.RealState = info.TransportState
					if info.TransportState == "PLAYING" {
						session.sawPlaying = true
					}
					if session.Duration <= 0 && info.TrackDuration > 0 {
						session.Duration = info.TrackDuration
					}
				}
			}
		}
	}

	position, _ := session.position(now)
	lesson := FindLessonInTree(course.Root, session.LessonPath)
	ended := session.finished(now)
	if ended {
		position = session.Duration
	}
	if position-session.LastSaved >= castSaveInterval || ended {
		session.LastSaved = position
		seconds := int(position)
		// A cast writes the same progress the player does - and just like the
		// player it never touches the completion flag until the lesson really
		// ended, so watching a finished lesson again keeps it completed.
		var completed *bool
		if ended {
			value := true
			completed = &value
		}
		if _, err := a.Progress.Update(course, session.LessonPath, completed, &seconds); err != nil {
			logf("dlna: cannot save the cast progress: %v", err)
		}
	}
	if !ended {
		a.cast.mutex.Unlock()
		return true
	}
	// The lesson finished: the cast carries on with the next one. Talking to
	// the renderer over HTTP is done without holding the lock.
	if lesson == nil {
		session.State = CastStateEnded
		session.watching = false
		a.cast.mutex.Unlock()
		// The lesson is gone from the course (or the course was re-scanned):
		// stop the device as well, instead of only stopping to look after it.
		a.stopCastDevice(session)
		return false
	}
	a.cast.mutex.Unlock()

	// Casting the next lesson talks to the renderer over HTTP: do it without
	// holding the lock.
	if err := a.castNext(session); err != nil {
		logf("dlna: the cast stops here: %v", err)
		a.cast.mutex.Lock()
		session.State = CastStateEnded
		session.watching = false
		a.cast.mutex.Unlock()
		// This was the last lesson (or the device could not be reached): nothing
		// is going to follow, so the device should stop too.
		a.stopCastDevice(session)
		return false
	}
	logf("dlna: %s continues with %q", session.Device, session.LessonTitle)
	return true
}

// castSeek jumps to a position inside the lesson that is playing.
//
// A plain file can be seeked on the device itself; a converted stream has no
// timeline the device could jump in, so ffmpeg is restarted with -ss and the
// device gets the new URL - that is what the progress bar of the browser does
// when it is clicked.
func (a *App) castSeek(session *CastSession, position float64) error {
	if session == nil {
		return errors.New("no cast is running")
	}
	if position < 0 {
		position = 0
	}
	if session.Duration > 0 && position > session.Duration-1 {
		// Stopping a second short of the end lets the watchdog still see the
		// lesson finish and continue with the next one.
		position = session.Duration - 1
		if position < 0 {
			position = 0
		}
	}
	course := a.Store.Get()
	if course == nil || course.Path != session.CoursePath {
		return errors.New("the cast belongs to another course")
	}
	lesson := FindLessonInTree(course.Root, session.LessonPath)
	if lesson == nil {
		return errors.New("lesson not found")
	}
	renderer, ok := a.DLNA.Lookup(session.UDN)
	if !ok {
		return errors.New("the cast device was not found on the network")
	}

	a.cast.mutex.Lock()
	wasPaused := !session.PausedAt.IsZero()
	a.cast.mutex.Unlock()

	if session.Converted {
		// Keep the stream that was already in use: "auto" would happily decide
		// differently now and restart a converted cast as a plain file.
		target, hasMedia, err := a.castTarget(session.BaseURL, course, lesson, "on", int(position))
		if err != nil {
			return err
		}
		if !hasMedia {
			return errors.New("this lesson has no media to cast")
		}
		if err := a.DLNA.Cast(renderer, target.Item, 0); err != nil {
			return err
		}
		// Casting always starts playing, so a paused cast has to be paused again.
		if wasPaused {
			_ = a.DLNA.Control(renderer, "pause")
		}
	} else if err := a.DLNA.Seek(renderer, int(position)); err != nil {
		return err
	}

	a.cast.mutex.Lock()
	if a.cast.session == session && !session.stopped {
		session.setPosition(position, time.Now(), wasPaused)
	}
	a.cast.mutex.Unlock()
	return nil
}

// castNext hands the next playable lesson to the device the session runs on and
// points the session at it, so the browser follows along.
func (a *App) castNext(session *CastSession) error {
	if session == nil {
		return errors.New("no cast is running")
	}
	course := a.Store.Get()
	if course == nil {
		return errors.New("no course loaded")
	}
	next := nextPlayableLesson(course, session.LessonPath)
	if next == nil {
		return errors.New("this was the last playable lesson of the course")
	}
	renderer, ok := a.DLNA.Lookup(session.UDN)
	if !ok {
		return errors.New("the cast device was not found on the network")
	}
	// Work out the length of the next lesson before it is handed over. This
	// runs on the watchdog's own clock, so it gets a considerably bigger budget
	// than a browser request would: on a cold disk ffprobe needs a while, and
	// without a length the cast can never tell when this lesson ended. The
	// answer is remembered (see Transcoder.Inspect), so the conversion
	// decision below reads the same facts without probing the file twice.
	file := a.mediaFileFor(course, next)
	duration := 0.0
	if file != "" {
		ctx, cancel := watchdogProbeContext()
		info, err := a.Transcoder.Probe(ctx, file)
		cancel()
		if err == nil {
			duration = info.Duration
		} else {
			logf("dlna: no length for %q (%v) - the device has to say when it ended", next.Title, err)
		}
	}
	target, hasMedia, err := a.castTarget(session.BaseURL, course, next, "auto", 0)
	if err != nil {
		return err
	}
	if !hasMedia {
		return errors.New("this lesson has no media to cast")
	}
	// Skipping ahead means this lesson is done: mark it exactly like the player
	// does when a video ends, so it counts towards the course progress.
	a.cast.mutex.Lock()
	finished := true
	seconds := int(session.estimate(time.Now()))
	a.cast.mutex.Unlock()
	if _, err := a.Progress.Update(course, session.LessonPath, &finished, &seconds); err != nil {
		logf("dlna: cannot mark %q as completed: %v", session.LessonTitle, err)
	}
	if err := a.DLNA.Cast(renderer, target.Item, 0); err != nil {
		return err
	}
	a.cast.mutex.Lock()
	if a.cast.session == session && !session.stopped {
		session.reset(next, file, duration, target.Converted, time.Now())
	}
	a.cast.mutex.Unlock()
	return nil
}

// stopCastDevice tells the device to stop playing. It is best effort: a TV that
// was switched off cannot be reached, and the cast is over either way - this
// only makes the end visible on the device too (the course has run out).
func (a *App) stopCastDevice(session *CastSession) {
	if session == nil {
		return
	}
	renderer, ok := a.DLNA.Lookup(session.UDN)
	if !ok {
		return
	}
	if err := a.DLNA.Control(renderer, "stop"); err != nil {
		logf("dlna: the device could not be stopped after %q: %v", session.LessonTitle, err)
		return
	}
	logf("dlna: %s stopped after %q (no lesson follows)", session.Device, session.LessonTitle)
}

// probeContext is the deadline for an ffprobe call that is not tied to a
// request (the watchdog runs on its own clock).
func probeContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

// watchdogProbeContext is the deadline for the ffprobe call that works out how
// long the next lesson plays. The watchdog hands the lesson over while the
// device idles, so nobody waits on this answer - but the cast cannot see the
// lesson end without it, which is why a cold disk deserves more than the
// request sized probeContext.
func watchdogProbeContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), castProbeTimeout)
}

// nextPlayableLesson is the lesson that follows path: the next one with media,
// because a document cannot be pushed to a TV.
func nextPlayableLesson(course *Course, path string) *Lesson {
	if course == nil || course.Root == nil {
		return nil
	}
	lessons := AllLessons(course.Root)
	position := -1
	for index, lesson := range lessons {
		if lesson.RelPath == path || lesson.URL == path {
			position = index
			break
		}
	}
	if position < 0 {
		return nil
	}
	for _, candidate := range lessons[position+1:] {
		if candidate.HasMedia() {
			return candidate
		}
	}
	return nil
}
