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
	Autoplay    bool
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

// finished reports whether the lesson has run to its end.
func (s *CastSession) finished(now time.Time) bool {
	if s.State == CastStateEnded || s.Duration <= 0 {
		return false
	}
	position, _ := s.position(now)
	if position >= s.Duration-castEndTolerance {
		return true
	}
	// A device that stopped (someone pressed stop on the remote) is done too.
	return s.RealState == "STOPPED" && !s.RealAt.IsZero() && now.Sub(s.RealAt) < castPositionFresh
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
	Autoplay       bool    `json:"autoplay"`
	Reported       bool    `json:"reported"` // the device told us, not the clock
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
		Autoplay:    session.Autoplay,
		Reported:    reported,
	}
	if course := a.Store.Get(); course != nil && course.Path == session.CoursePath {
		if next := nextPlayableLesson(course, session.LessonPath); next != nil {
			view.NextTitle = next.Title
			view.NextLessonPath = next.RelPath
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
			return
		}
	}
}

// castTick is one watchdog round. It reports false when the session is over
// (stopped, replaced, or finished without a next lesson to play).
func (a *App) castTick(session *CastSession) bool {
	a.cast.mutex.Lock()
	if session == nil || session.stopped || a.cast.session != session {
		a.cast.mutex.Unlock()
		return false
	}
	now := time.Now()
	if now.Sub(session.StartedAt) > castPositionMaxAge {
		session.stopped = true
		a.cast.mutex.Unlock()
		return false
	}
	course := a.Store.Get()
	if course == nil {
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
					session.RealPosition = info.RelTime
					session.RealAt = now
					session.RealState = info.TransportState
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
	if !session.Autoplay || lesson == nil {
		session.State = CastStateEnded
		a.cast.mutex.Unlock()
		return false
	}
	a.cast.mutex.Unlock()

	// Casting the next lesson talks to the renderer over HTTP: do it without
	// holding the lock.
	if err := a.castNext(session); err != nil {
		logf("dlna: the cast stops here: %v", err)
		a.cast.mutex.Lock()
		session.State = CastStateEnded
		a.cast.mutex.Unlock()
		return false
	}
	logf("dlna: %s continues with %q", session.Device, session.LessonTitle)
	return true
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
	file := a.mediaFileFor(course, next)
	duration := 0.0
	ctx, cancel := probeContext()
	if info, err := a.Transcoder.Probe(ctx, file); err == nil {
		duration = info.Duration
	}
	cancel()
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

// probeContext is the deadline for an ffprobe call that is not tied to a
// request (the watchdog runs on its own clock).
func probeContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
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
