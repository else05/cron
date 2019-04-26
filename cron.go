package cron

import (
	"errors"
	"log"
	"runtime"
	"sort"
	"time"
)

// Cron keeps track of any number of entries, invoking the associated func as
// specified by the schedule. It may be started, stopped, and the entries may
// be inspected while running.
type Cron struct {
	entries  []*Entry
	stop     chan struct{}
	add      chan *Entry
	remove   chan string
	snapshot chan []*Entry
	running  bool
	ErrorLog *log.Logger
	location *time.Location
}

// Job is an interface for submitted cron jobs.
type Job interface {
	Run()
}

// The Schedule describes a job's duty cycle.
type Schedule interface {
	// Return the next activation time, later than the given time.
	// Next is invoked initially, and then each time the job is run.
	Next(time.Time) time.Time

	RandomNext(time.Time, int) time.Time
}

// Entry consists of a schedule and the func to execute on that schedule.
type Entry struct {
	// The schedule on which this job should be run.
	Schedule Schedule

	// The next time the job will run. This is the zero time if Cron has not been
	// started or this entry's schedule is unsatisfiable
	Next time.Time

	// The last time this job was run. This is the zero time if the job has never
	// been run.
	Prev time.Time

	// The Job to run.
	Job Job

	// Unique name to identify the Entry so as to be able to remove it later.
	Name string

	// 随机延迟的范围,以DelayRange为最大范围生成一个随机数R，让下一次执行延迟R秒，单位 秒 ，范围 (0,DelayRange)
	DelayRange int
}

// byTime is a wrapper for sorting the entry array by time
// (with zero time at the end).
type byTime []*Entry

func (s byTime) Len() int      { return len(s) }
func (s byTime) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s byTime) Less(i, j int) bool {
	// Two zero times should return false.
	// Otherwise, zero is "greater" than any other time.
	// (To sort it at the end of the list.)
	if s[i].Next.IsZero() {
		return false
	}
	if s[j].Next.IsZero() {
		return true
	}
	return s[i].Next.Before(s[j].Next)
}

// New returns a new Cron job runner, in the Local time zone.
func New() *Cron {
	return NewWithLocation(time.Now().Location())
}

// NewWithLocation returns a new Cron job runner.
func NewWithLocation(location *time.Location) *Cron {
	return &Cron{
		entries:  nil,
		add:      make(chan *Entry),
		remove:   make(chan string),
		stop:     make(chan struct{}),
		snapshot: make(chan []*Entry),
		running:  false,
		ErrorLog: nil,
		location: location,
	}
}

// A wrapper that turns a func() into a cron.Job
type FuncJob func()

func (f FuncJob) Run() { f() }

// AddFunc adds a func to the Cron to be run on the given schedule.
func (c *Cron) AddNameFunc(name string, spec string, cmd func()) error {
	return c.AddNameJob(name, spec, FuncJob(cmd))
}
func (c *Cron) AddFunc(spec string, cmd func()) error {
	return c.AddJob(spec, FuncJob(cmd))
}
func (c *Cron) AddDelayFunc(spec string, delayRange int, cmd func()) error {
	if delayRange < 0 || delayRange > 82800 {
		return errors.New("时间不能超过(0,82800)秒（24H）")
	}
	return c.AddDelayJob(spec, delayRange, FuncJob(cmd))
}

// AddJob adds a Job to the Cron to be run on the given schedule.
func (c *Cron) AddJob(spec string, cmd Job) error {
	return c.AddNameJob("", spec, cmd)
}

func (c *Cron) AddNameJob(name string, spec string, cmd Job) error {
	schedule, err := Parse(spec)
	if err != nil {
		return err
	}
	c.NameAndDelaySchedule(name, schedule, 0, cmd)
	return nil
}

func (c *Cron) AddDelayJob(spec string, delayRange int, cmd Job) error {
	if delayRange < 0 || delayRange > 82800 {
		return errors.New("时间不能超过(0,82800)秒（24H）")
	}
	schedule, err := Parse(spec)
	if err != nil {
		return err
	}
	c.NameAndDelaySchedule("", schedule, 0, cmd)
	return nil
}

// RemoveJob removes a Job from the Cron based on name.
func (c *Cron) RemoveJob(name string) {
	if c.running {
		c.remove <- name
		return
	}

	i := pos(c.entries, name)
	if i == -1 {
		return
	}
	c.entries = removeEntry(c.entries, i)
}

func removeEntry(entries []*Entry, index int) []*Entry {
	target := entries[:0]
	for i, v := range entries {
		if i != index {
			target = append(target, v)
		}
	}
	return target
}

func pos(entrySlice []*Entry, name string) int {
	for p, e := range entrySlice {
		if e.Name == name {
			return p
		}
	}
	return -1
}

// Schedule adds a Job to the Cron to be run on the given schedule.
func (c *Cron) Schedule(schedule Schedule, cmd Job) {
	c.NameAndDelaySchedule("", schedule, 0, cmd)
}

func (c *Cron) NameAndDelaySchedule(name string, schedule Schedule, delayRange int, cmd Job) {
	if delayRange < 0 || delayRange > 82800 {
		delayRange = 0
	}
	entry := &Entry{
		Schedule:   schedule,
		Job:        cmd,
		Name:       name,
		DelayRange: delayRange,
	}
	if !c.running {
		c.entries = append(c.entries, entry)
		return
	}

	c.add <- entry
}

// Entries returns a snapshot of the cron entries.
func (c *Cron) Entries() []*Entry {
	if c.running {
		c.snapshot <- nil
		x := <-c.snapshot
		return x
	}
	return c.entrySnapshot()
}

// Location gets the time zone location
func (c *Cron) Location() *time.Location {
	return c.location
}

// Start the cron scheduler in its own go-routine, or no-op if already started.
func (c *Cron) Start() {
	if c.running {
		return
	}
	c.running = true
	go c.run()
}

// Run the cron scheduler, or no-op if already running.
func (c *Cron) Run() {
	if c.running {
		return
	}
	c.running = true
	c.run()
}

func (c *Cron) runWithRecovery(j Job) {
	defer func() {
		if r := recover(); r != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			c.logf("cron: panic running job: %v\n%s", r, buf)
		}
	}()
	j.Run()
}

// Run the scheduler. this is private just due to the need to synchronize
// access to the 'running' state variable.
func (c *Cron) run() {
	// Figure out the next activation times for each entry.
	now := c.now()
	for _, entry := range c.entries {
		entry.Next = entry.Schedule.RandomNext(now, entry.DelayRange)
	}

	for {
		// Determine the next entry to run.
		sort.Sort(byTime(c.entries))

		var timer *time.Timer
		if len(c.entries) == 0 || c.entries[0].Next.IsZero() {
			// If there are no entries yet, just sleep - it still handles new entries
			// and stop requests.
			timer = time.NewTimer(100000 * time.Hour)
		} else {
			timer = time.NewTimer(c.entries[0].Next.Sub(now))
		}

		for {
			select {
			case now = <-timer.C:
				now = now.In(c.location)
				// Run every entry whose next time was less than now
				for _, e := range c.entries {
					if e.Next.After(now) || e.Next.IsZero() {
						break
					}
					go c.runWithRecovery(e.Job)
					e.Prev = e.Next
					e.Next = e.Schedule.RandomNext(now, e.DelayRange)
				}

			case newEntry := <-c.add:
				if newEntry.Name != "" && pos(c.entries, newEntry.Name) != -1 {
					continue // 已经存在同名任务
				}

				timer.Stop()
				now = c.now()
				newEntry.Next = newEntry.Schedule.RandomNext(now, newEntry.DelayRange)
				c.entries = append(c.entries, newEntry)

			case name := <-c.remove:
				i := pos(c.entries, name)
				if i == -1 {
					continue
				}
				timer.Stop()
				c.entries = removeEntry(c.entries, i)

			case <-c.snapshot:
				c.snapshot <- c.entrySnapshot()
				continue

			case <-c.stop:
				timer.Stop()
				return
			}

			break
		}
	}
}

// Logs an error to stderr or to the configured error log
func (c *Cron) logf(format string, args ...interface{}) {
	if c.ErrorLog != nil {
		c.ErrorLog.Printf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

// Stop stops the cron scheduler if it is running; otherwise it does nothing.
func (c *Cron) Stop() {
	if !c.running {
		return
	}
	c.stop <- struct{}{}
	c.running = false
}

// entrySnapshot returns a copy of the current cron entry list.
func (c *Cron) entrySnapshot() []*Entry {
	entries := []*Entry{}
	for _, e := range c.entries {
		entries = append(entries, &Entry{
			Schedule: e.Schedule,
			Next:     e.Next,
			Prev:     e.Prev,
			Job:      e.Job,
			Name:     e.Name,
		})
	}
	return entries
}

// now returns current time in c location
func (c *Cron) now() time.Time {
	return time.Now().In(c.location)
}
