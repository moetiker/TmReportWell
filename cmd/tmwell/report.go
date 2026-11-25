package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Report is the rendered mail.
type Report struct{ Subject, Body string }

const (
	levelOK = iota
	levelWarn
	levelBad
)

var levelName = []string{"OK", "WARNING", "PROBLEM"}

type reportInput struct {
	Host         string
	From, To     time.Time
	PrefsFrom    time.Time // completed backups after this are reported
	AttemptsFrom time.Time // started backups after this are reported
	State        *TMState  // nil when the preferences could not be read
	StateErr     error
	LogErr       error
	Gaps         []Gap
	Groups       map[string]*Group
	WarnDays     float64
	BadDays      float64
}

func stamp(t time.Time) string { return t.Local().Format("Mon 2006-01-02 15:04") }

func age(d time.Duration) string {
	switch {
	case d < 2*time.Hour:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	}
	return fmt.Sprintf("%d days", int(d.Hours()/24))
}

func gb(n int64) string {
	if n >= 1e12 {
		return fmt.Sprintf("%.2f TB", float64(n)/1e12)
	}
	return fmt.Sprintf("%.0f GB", float64(n)/1e9)
}

// finished reports whether attempt i of d led to a completed backup before the
// next attempt.
func finished(d Destination, i int) bool {
	last := i == len(d.Attempts)-1
	for _, s := range d.Snapshots {
		if !s.Before(d.Attempts[i]) && (last || s.Before(d.Attempts[i+1])) {
			return true
		}
	}
	return false
}

// openAttempt returns the attempt in (from, to] that may still be running: the
// newest one over all disks without a completed backup. Without an answer from
// tmutil it counts as running, so a later report settles it.
func openAttempt(st *TMState, from, to time.Time) (time.Time, bool) {
	var open time.Time
	if st.RunningKnown && !st.Running {
		return open, false
	}
	for _, d := range st.Dest {
		i := len(d.Attempts) - 1
		if i < 0 || finished(d, i) {
			continue
		}
		if a := d.Attempts[i]; a.After(from) && !a.After(to) && a.After(open) {
			open = a
		}
	}
	return open, !open.IsZero()
}

// unfinished returns the attempts in (from, to] that led to no backup, without
// the one that is still open.
func unfinished(d Destination, from, to, open time.Time) []time.Time {
	var r []time.Time
	for i, a := range d.Attempts {
		if a.After(from) && !a.After(to) && !a.Equal(open) && !finished(d, i) {
			r = append(r, a)
		}
	}
	return r
}

// nextAttempt works out from the scheduler's own words when macOS tries again.
// skipped is the reason when the counted attempt was skipped.
func nextAttempt(st *TMState, notDue, skips []*Group) (attempt, next time.Time, skipped string) {
	for _, g := range notDue {
		i := strings.Index(g.Text, "last attempt ")
		if i < 0 {
			continue
		}
		t, err := time.Parse("2006-01-02 15:04:05 -0700", g.Text[i+len("last attempt "):])
		if err == nil && t.After(attempt) {
			attempt = t
		}
	}
	if attempt.IsZero() || st.Interval == 0 {
		return time.Time{}, time.Time{}, ""
	}
	for _, g := range skips {
		// The skip is logged about 30 seconds after the attempt it belongs to.
		if d := g.Last.Sub(attempt); d >= 0 && d < 2*time.Minute {
			skipped = g.Text
		}
	}
	return attempt, attempt.Add(st.Interval), skipped
}

func yes(b *bool) bool { return b != nil && *b }
func no(b *bool) bool  { return b != nil && !*b }

func buildReport(in reportInput) Report {
	level := levelOK
	raise := func(l int) {
		if l > level {
			level = l
		}
	}
	var headline string
	var situation, todo, done, notDone, info, other []string

	byKind := map[string][]*Group{}
	for _, g := range in.Groups {
		byKind[g.Kind] = append(byKind[g.Kind], g)
	}
	for _, gs := range byKind {
		sort.Slice(gs, func(i, j int) bool { return gs[i].First.Before(gs[j].First) })
	}

	if in.State == nil {
		raise(levelBad)
		headline = "backup state unknown"
		situation = append(situation, "It is not known whether backups work: the Time Machine preferences could not be read.")
		todo = append(todo, "Give tmwell Full Disk Access in System Settings, Privacy & Security.")
		notDone = append(notDone, fmt.Sprintf("Time Machine preferences could not be read: %v", in.StateErr))
	} else {
		st := in.State
		open, isOpen := openAttempt(st, in.AttemptsFrom, in.To)
		running := st.RunningKnown && st.Running
		worst, completed := -1.0, 0
		anyStale := false
		for _, d := range st.Dest {
			for _, s := range d.Snapshots {
				if s.After(in.PrefsFrom) && !s.After(in.To) {
					completed++
					done = append(done, fmt.Sprintf("%s  backup completed on %q", stamp(s), d.Name))
				}
			}
			for _, a := range unfinished(d, in.AttemptsFrom, in.To, open) {
				raise(levelWarn)
				notDone = append(notDone, fmt.Sprintf("%s  backup started on %q but did not complete", stamp(a), d.Name))
			}
			line := fmt.Sprintf("%q: %s free, %s used", d.Name, gb(d.Free), gb(d.Used))
			info = append(info, line)

			// Every disk is judged on its own: a fresh one must not hide a stale one.
			days, text := 1e9, "no completed backup"
			if n := len(d.Snapshots); n > 0 {
				last := d.Snapshots[n-1]
				days, text = in.To.Sub(last).Hours()/24, "last backup "+age(in.To.Sub(last))+" ago"
				situation = append(situation, fmt.Sprintf("The last backup on %q completed %s, %s ago.", d.Name, stamp(last), age(in.To.Sub(last))))
			} else {
				situation = append(situation, fmt.Sprintf("%q has no completed backup on record.", d.Name))
			}
			if days > worst {
				worst, headline = days, text
				if len(st.Dest) > 1 {
					headline = fmt.Sprintf("%q %s", d.Name, text)
				}
			}
			stale := days >= in.WarnDays
			anyStale = anyStale || stale
			if days >= in.BadDays {
				raise(levelBad)
			} else if stale {
				raise(levelWarn)
			}
			if no(d.Attached) {
				situation = append(situation, fmt.Sprintf("%q is not connected at the time of this report.", d.Name))
				if stale && !running {
					todo = append(todo, fmt.Sprintf("Connect the disk %q.", d.Name))
				}
			}
		}
		if len(st.Dest) == 0 {
			raise(levelBad)
			headline = "no backup disk configured"
			situation = append(situation, "No backup disk is configured.")
			todo = append(todo, "Add a backup disk in System Settings, General, Time Machine.")
		} else if completed == 0 && !running {
			situation = append(situation, "No backup completed since the last report.")
		}
		switch {
		case running:
			situation = append(situation, "A backup is running right now.")
		case isOpen:
			situation = append(situation, fmt.Sprintf("The outcome of the backup started %s is not known yet.", stamp(open)))
		}
		if !st.AutoBackup {
			raise(levelWarn)
			situation = append(situation, "Automatic backups are switched off.")
			todo = append(todo, "Switch automatic backups on in System Settings, General, Time Machine.")
		}
		if st.RequiresAC && yes(st.OnBattery) {
			situation = append(situation, "The Mac runs on battery, and automatic backups need AC power.")
			if anyStale && !running {
				todo = append(todo, "Connect the power adapter.")
			}
		}
		if attempt, next, skipped := nextAttempt(st, byKind[kindNotDue], byKind[kindSkip]); st.AutoBackup && !running && next.After(in.To) {
			s := fmt.Sprintf("macOS does not try again by itself before %s.", stamp(next))
			if skipped != "" {
				s = fmt.Sprintf("The attempt of %s was skipped (%s). %s", stamp(attempt), skipped, s)
			}
			situation = append(situation, s)
		}
		if anyStale && !running && len(st.Dest) > 0 {
			todo = append(todo, "Choose Back Up Now in the Time Machine menu.")
		}
		s := "Automatic backups: every " + age(st.Interval)
		if st.Interval == 0 {
			s = "Automatic backups: interval not set"
		}
		if st.RequiresAC {
			s += ", only on AC power"
		}
		info = append(info, s)
		if n := len(st.LocalSnaps); n > 0 {
			info = append(info, fmt.Sprintf("Local snapshots on this Mac, no replacement for a backup: %d, newest %s", n, stamp(st.LocalSnaps[n-1])))
		}
	}

	span := func(g *Group) string {
		if g.Count == 1 {
			return stamp(g.First)
		}
		return fmt.Sprintf("%s to %s, %d times", stamp(g.First), stamp(g.Last), g.Count)
	}
	for _, g := range byKind[kindSkip] {
		notDone = append(notDone, fmt.Sprintf("Backup skipped: %s (%s)", g.Text, span(g)))
	}
	for _, g := range byKind[kindNoDisk] {
		name := strings.TrimPrefix(g.Text, "backup disk not mounted ")
		if in.State != nil {
			for _, d := range in.State.Dest {
				for _, u := range d.UUIDs {
					if u == name {
						name = fmt.Sprintf("%q", d.Name)
					}
				}
			}
		}
		notDone = append(notDone, fmt.Sprintf("Backup disk %s not mounted (%s)", name, span(g)))
	}
	if n := len(byKind[kindProblem]); n > 0 {
		raise(levelWarn)
		situation = append(situation, "Time Machine logged errors, see the log messages below.")
	}
	for _, g := range append(byKind[kindProblem], byKind[kindOther]...) {
		tag := ""
		if g.Kind == kindProblem {
			tag = "ERROR "
		}
		txt := g.Text
		if r := []rune(txt); len(r) > 300 {
			txt = string(r[:300]) + "..."
		}
		other = append(other, fmt.Sprintf("%s%s (%s)", tag, txt, span(g)))
	}
	if n := len(other); n > 40 {
		other = append(other[:40], fmt.Sprintf("%d more not shown", n-40))
	}
	n := 0
	for _, g := range byKind[kindNotDue] {
		n += g.Count
	}
	if n > 0 {
		info = append(info, fmt.Sprintf("Scheduler checks that found no backup due: %d", n))
	}
	if in.LogErr != nil {
		raise(levelWarn)
		situation = append(situation, "The system log could not be read, so reasons for skipped backups may be missing.")
		notDone = append(notDone, fmt.Sprintf("The system log could not be read: %v", in.LogErr))
	}
	for _, g := range in.Gaps {
		situation = append(situation, fmt.Sprintf("Log messages from %s to %s may be missing: tmwell did not run, and macOS keeps them for about a day.", stamp(g.From), stamp(g.To)))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Time Machine on %s: %s, %s\n", in.Host, levelName[level], headline)
	section := func(title string, lines []string, empty string) {
		if len(lines) == 0 {
			if empty == "" {
				return
			}
			lines = []string{empty}
		}
		fmt.Fprintf(&b, "\n%s\n", title)
		for _, l := range lines {
			fmt.Fprintf(&b, "  - %s\n", l)
		}
	}
	section("Situation", situation, "")
	section("What to do", todo, "nothing")
	fmt.Fprintf(&b, "\nDetails for %s to %s\n", stamp(in.From), stamp(in.To))
	section("Done", done, "no backup completed in this period")
	section("Not done", notDone, "nothing to report")
	section("Settings and disks", info, "")
	section("Other log messages", other, "")
	return Report{
		Subject: fmt.Sprintf("[TmWell] %s %s: %s", in.Host, levelName[level], headline),
		Body:    b.String(),
	}
}
