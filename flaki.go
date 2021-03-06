// Package flaki provides the implementation of Flaki - Das kleine Generator.
// Flaki is a distributed unique IDs generator inspired by Snowflake (https://github.com/twitter/snowflake).
// It returns unique IDs of type uint64 or string. The ID is composed of: 5-bit component ID, 2-bit node ID,
// 15-bit sequence number, and 42-bit time's milliseconds since the epoch.
// Unique IDs will be generated until 139 years 4 months and a few days after the epoch. After that, there
// will be an overflow and the newly generated IDs won't be unique anymore.
package flaki

import (
	"sync"

	"fmt"
	"strconv"
	"time"
)

const (
	componentIDBits  = 5
	nodeIDNodeIDBits = 2
	sequenceBits     = 15
	timestampBits    = 64 - componentIDBits - nodeIDNodeIDBits - sequenceBits

	maxComponentID = (1 << componentIDBits) - 1
	maxNodeID      = (1 << nodeIDNodeIDBits) - 1
	sequenceMask   = (1 << sequenceBits) - 1

	componentIDShift   = sequenceBits
	nodeIDNodeIDShift  = sequenceBits + componentIDBits
	timestampLeftShift = sequenceBits + componentIDBits + nodeIDNodeIDBits
)

// Flaki is the unique ID generator.
type Flaki struct {
	componentID   uint64
	nodeIDNodeID  uint64
	lastTimestamp int64
	sequence      uint64
	mutex         *sync.Mutex

	// startEpoch is the reference time from which we count the elapsed time.
	// The default is 01.01.2017 00:00:00 +0000 UTC.
	startEpoch time.Time

	// timeGen is the function that returns the current time.
	timeGen func() time.Time
}

// Option type is use to configure the Flaki generator. It takes one argument: the Flaki we are operating on.
type Option func(*Flaki) error

// New returns a new unique IDs generator.
//
// If you do not specify options, Flaki will use the following
// default parameters: 0 for the node ID, 0 for the component ID,
// and 01.01.2017 as start epoch.
//
// To change the default settings, use the options in the call
// to New, i.e. New(logger, ComponentID(1), NodeID(2), StartEpoch(startEpoch))
func New(options ...Option) (*Flaki, error) {

	var flaki = &Flaki{
		componentID:   0,
		nodeIDNodeID:  0,
		startEpoch:    time.Date(2017, 1, 1, 0, 0, 0, 0, time.UTC),
		lastTimestamp: -1,
		sequence:      0,
		timeGen:       time.Now,
		mutex:         &sync.Mutex{},
	}

	// Apply options to the Generator.
	for _, opt := range options {
		var err = opt(flaki)
		if err != nil {
			return nil, err
		}
	}

	return flaki, nil
}

// NextID returns a new unique ID, or an error if the clock moves backward.
func (f *Flaki) NextID() (uint64, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()

	var timestamp = f.currentTimeInUnixMillis()
	var prevTimestamp = f.lastTimestamp

	if timestamp < prevTimestamp {
		return 0, fmt.Errorf("clock moved backwards. Refusing to generate IDs for %d [ms]", prevTimestamp-timestamp)
	}

	// If too many IDs (more than 2^sequenceBits) are requested in a given time unit (millisecond),
	// the sequence overflows. If it happens, we wait till the next time unit to generate new IDs,
	// otherwise we end up with duplicates.
	if timestamp == prevTimestamp {
		f.sequence = (f.sequence + 1) & sequenceMask
		if f.sequence == 0 {
			timestamp = f.tilNextMillis(prevTimestamp)
		}
	} else {
		f.sequence = 0
	}

	f.lastTimestamp = timestamp
	var id = (uint64(timestamp-timeToUnixMillis(f.startEpoch)) << timestampLeftShift) |
		(f.nodeIDNodeID << nodeIDNodeIDShift) | (f.componentID << componentIDShift) | f.sequence

	return id, nil
}

// NextIDString returns the NextID as a string.
func (f *Flaki) NextIDString() (string, error) {
	var id, err = f.NextID()
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(id, 10), nil
}

// NextValidID always returns a new unique ID, it never returns an error.
// If the clock moves backward, it waits until the situation goes back to normal
// and then returns the valid ID.
func (f *Flaki) NextValidID() uint64 {
	var id uint64
	var err = fmt.Errorf("")

	// We wait until we get a valid ID
	for err != nil {
		id, err = f.NextID()
	}

	return id
}

// NextValidIDString returns the NextValidID as a string.
func (f *Flaki) NextValidIDString() string {
	var id = f.NextValidID()
	return strconv.FormatUint(id, 10)
}

// tilNextMillis waits until the next millisecond.
func (f *Flaki) tilNextMillis(prevTimestamp int64) int64 {
	var timestamp = f.currentTimeInUnixMillis()

	for timestamp <= prevTimestamp {
		timestamp = f.currentTimeInUnixMillis()
	}
	return timestamp
}

// epochValidity returns the date till which Flaki can generate valid IDs.
func epochValidity(startEpoch time.Time) time.Time {
	var durationMilliseconds int64 = (1 << timestampBits) - 1
	var durationNanoseconds = durationMilliseconds * 1e6

	var validityDuration = time.Duration(durationNanoseconds)
	var validUntil = startEpoch.Add(validityDuration)
	return validUntil
}

func (f *Flaki) currentTimeInUnixMillis() int64 {
	return timeToUnixMillis(f.timeGen())
}

func timeToUnixMillis(t time.Time) int64 {
	return t.UnixNano() / 1e6
}

// ComponentID is the option used to set the component ID.
func ComponentID(id uint64) Option {
	return func(f *Flaki) error {
		if id > maxComponentID {
			return fmt.Errorf("the component id must be in [%d..%d]", 0, maxComponentID)
		}
		f.componentID = id
		return nil
	}
}

// NodeID is the option used to set the node ID.
func NodeID(id uint64) Option {
	return func(f *Flaki) error {
		if id > maxNodeID {
			return fmt.Errorf("the node id must be in [%d..%d]", 0, maxNodeID)
		}
		f.nodeIDNodeID = id
		return nil
	}
}

// StartEpoch is the option used to set the epoch.
func StartEpoch(epoch time.Time) Option {
	return func(f *Flaki) error {
		// According to time.Time documentation, UnixNano returns the number of nanoseconds elapsed
		// since January 1, 1970 UTC. The result is undefined if the Unix time in nanoseconds cannot
		// be represented by an int64 (i.e. a date after 2262).
		if epoch.Before(time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)) ||
			epoch.After(time.Date(2262, 1, 1, 0, 0, 0, 0, time.UTC)) {
			return fmt.Errorf("the epoch must be between 01.01.1970 and 01.01.2262")
		}
		f.startEpoch = epoch
		return nil
	}
}

// setTimeGen set the function that returns the current time. It is used in the tests
// to control the time.
func (f *Flaki) setTimeGen(timeGen func() time.Time) {
	f.timeGen = timeGen
}
