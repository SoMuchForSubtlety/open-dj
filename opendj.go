package opendj

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Dj stores the queue and handlers
type Dj struct {
	waitingQueue queue
	currentEntry QueueEntry

	runningCommand *exec.Cmd

	handlers handlers

	songStarted time.Time
}

type handlers struct {
	newSongHandler   func(QueueEntry)
	endOfSongHandler func(QueueEntry, error)
	errorHander      func(error)
}

// Media represents a video or song that can be streamed.
//
// this can be anything youtube-dl supports.
type Media struct {
	Title    string
	URL      string
	Duration time.Duration
}

// A QueueEntry represents media and metadata the can be ented into a queue.
type QueueEntry struct {
	Media      Media
	Owner      string
	Dedication string
}

type queue struct {
	Items []QueueEntry
	sync.Mutex
}

// NewDj initializes and returns a new Dj struct.
func NewDj(queue []QueueEntry) (dj *Dj) {
	_, err := exec.LookPath("yt-dlp")
	if err != nil {
	  panic(err)
	}

	_, err = exec.LookPath("ffmpeg")
	if err != nil {
	  panic(err)
	}

	dj = &Dj{}
	dj.waitingQueue.Items = queue

	return dj
}

// AddNewSongHandler adds a function that will be called every time a new song starts playing.
func (dj *Dj) AddNewSongHandler(f func(QueueEntry)) {
	dj.handlers.newSongHandler = f
}

// AddEndOfSongHandler adds a function that will be called every time a song stops playing.
// It gets passed the QueueEntry that finished playing and any errors encountered during playback.
func (dj *Dj) AddEndOfSongHandler(f func(QueueEntry, error)) {
	dj.handlers.endOfSongHandler = f
}

// AddPlaybackErrorHandler adds a function that will be called every time an error occurs during playback.
//
// In effect this mean it will be called every time ffmpeg or yt-dlp exit with an error.
// Sometimes ffmpeg can exit with code 1 even though the song was streamed successfully.
func (dj *Dj) AddPlaybackErrorHandler(f func(error)) {
	dj.handlers.errorHander = f
}

// Queue return the current queue as a list of queue entries.
func (dj *Dj) Queue() []QueueEntry {
	return dj.waitingQueue.Items
}

// AddEntry adds the passed QueueEntry at the end of the queue.
func (dj *Dj) AddEntry(newEntry QueueEntry) {
	dj.waitingQueue.Lock()
	dj.waitingQueue.Items = append(dj.waitingQueue.Items, newEntry)
	dj.waitingQueue.Unlock()
}

// InsertEntry inserts the passed QueueEntry into the queue at the given index.
//
// if the index is too high it has the same effect as AddEntry().
// returns an error if the index is < 0.
func (dj *Dj) InsertEntry(newEntry QueueEntry, index int) error {
	dj.waitingQueue.Lock()
	defer dj.waitingQueue.Unlock()

	if index < 0 {
		return errors.New("index out of range")
	} else if index >= len(dj.waitingQueue.Items) {
		dj.waitingQueue.Items = append(dj.waitingQueue.Items, newEntry)
		return nil
	}
	dj.waitingQueue.Items = append(dj.waitingQueue.Items, QueueEntry{})
	copy(dj.waitingQueue.Items[index+1:], dj.waitingQueue.Items[index:])
	dj.waitingQueue.Items[index] = newEntry
	return nil
}

// RemoveIndex removes the element the given index from the queue
//
// returns an error if the index is out of range.
func (dj *Dj) RemoveIndex(index int) error {
	dj.waitingQueue.Lock()
	defer dj.waitingQueue.Unlock()
	if index >= len(dj.waitingQueue.Items) || index < 0 {
		return errors.New("index out of range")
	}
	dj.waitingQueue.Items = append(dj.waitingQueue.Items[:index], dj.waitingQueue.Items[index+1:]...)
	return nil
}

// ChangeIndex swaps the QueueEntry the index for the provided one
//
// returns an error if the index is out of range
func (dj *Dj) ChangeIndex(newEntry QueueEntry, index int) error {
	dj.waitingQueue.Lock()
	defer dj.waitingQueue.Unlock()

	if index < 0 || index >= len(dj.waitingQueue.Items) {
		return errors.New("index out of range")
	}

	dj.waitingQueue.Items[index] = newEntry

	return nil
}

func (dj *Dj) pop() (QueueEntry, error) {
	dj.waitingQueue.Lock()
	defer dj.waitingQueue.Unlock()

	if len(dj.waitingQueue.Items) < 1 {
		return QueueEntry{}, errors.New("can't pop from empty queue")
	}

	entry := dj.waitingQueue.Items[0]
	dj.waitingQueue.Items = dj.waitingQueue.Items[1:]
	return entry, nil
}

// EntryAtIndex returns the QueueEntry at the given index or error if the index is out of range
func (dj *Dj) EntryAtIndex(index int) (QueueEntry, error) {
	dj.waitingQueue.Lock()
	defer dj.waitingQueue.Unlock()

	if index >= len(dj.waitingQueue.Items) || index < 0 {
		return QueueEntry{}, errors.New("index out of range")
	}

	entry := dj.waitingQueue.Items[index]

	return entry, nil
}

// Play starts the playback to the given RTMP server.
//
// If nothing is in the playlist it waits for new content to be added.
// Any encoutered errors are handled by the errorHandler.
func (dj *Dj) Play(rtmpServer string) {
	for {
		entry, err := dj.pop()
		if err != nil {
			// TODO: not ideal, maybe have a backup playlist
			time.Sleep(time.Second * 5)
			continue
		}

		dj.currentEntry = entry

		if dj.handlers.newSongHandler != nil {
			dj.handlers.newSongHandler(entry)
		}

		command := exec.Command("yt-dlp", "-f", "bestaudio", "-g", dj.currentEntry.Media.URL)
		url, err := command.Output()
		if err != nil {
			if dj.handlers.errorHander != nil {
				dj.handlers.errorHander(err)
			}
			if dj.handlers.endOfSongHandler != nil {
				dj.handlers.endOfSongHandler(entry, err)
			}
			continue
		}

		urlProper := strings.TrimSpace(string(url))
		dj.songStarted = time.Now()

		command = exec.Command("ffmpeg", "-loglevel", "warning", "-hide_banner", "-reconnect", "1", "-reconnect_at_eof", "1", "-reconnect_delay_max", "3", "-re", "-i", urlProper, "-codec:a", "aac", "-f", "flv", rtmpServer)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		err = command.Start()
		if err != nil {
			if dj.handlers.errorHander != nil {
				dj.handlers.errorHander(err)
			}
			if dj.handlers.endOfSongHandler != nil {
				dj.handlers.endOfSongHandler(entry, err)
			}
			continue
		}

		dj.runningCommand = command

		err = command.Wait()
		if err != nil {
			if dj.handlers.errorHander != nil {
				dj.handlers.errorHander(err)
			}
		}

		if dj.handlers.endOfSongHandler != nil {
			dj.handlers.endOfSongHandler(entry, err)
		}

		dj.runningCommand = nil
	}
}

// UserPosition returns a slice of all the position in the queue that belong to the given user.
func (dj *Dj) UserPosition(nick string) (positions []int) {
	dj.waitingQueue.Lock()
	defer dj.waitingQueue.Unlock()

	for i, content := range dj.waitingQueue.Items {
		if content.Owner == nick {
			positions = append(positions, i)
		}
	}
	return positions
}

// DurationUntilUser returns a slice of all the durations to the songs in the queue that belong to the given user.
func (dj *Dj) DurationUntilUser(nick string) (durations []time.Duration) {
	dj.waitingQueue.Lock()
	defer dj.waitingQueue.Unlock()

	dur := dj.currentEntry.Media.Duration - time.Since(dj.songStarted)
	for _, content := range dj.waitingQueue.Items {
		if content.Owner == nick {
			durations = append(durations, dur)
		}
		dur += content.Media.Duration
	}
	return durations
}

// CurrentlyPlaying returns the song that is currently being played and for how long it has been playing.
//
// Returns an error if there is nothing playing.
func (dj *Dj) CurrentlyPlaying() (entry QueueEntry, progress time.Duration, err error) {
	if dj.currentEntry.Media == (Media{}) {
		err = errors.New("there is no song being played")
	}
	return dj.currentEntry, time.Since(dj.songStarted), err
}
