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
	Host     string
	From, To time.Time
	State    *TMState // nil when the preferences could not be read
	StateErr error
	LogErr   error
	Groups   map[string]*Group
	WarnDays float64
	BadDays  float64
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

// unfinished returns the attempts in (from, to] that no completed backup
// follows before the next attempt. The newest one is left out while a backup
// is running.
func unfinished(d Destination, from, to time.Time, running bool) []time.Time {
	var r []time.Time
	for i, a := range d.Attempts {
		if !a.After(from) || a.After(to) {
			continue
		}
		last := i == len(d.Attempts)-1
		if last && running {
			continue
		}
		done := false
		for _, s := range d.Snapshots {
			if !s.Before(a) && (last || s.Before(d.Attempts[i+1])) {
				done = true
			}
		}
		if !done {
			r = append(r, a)
		}
	}
	return r
}

func buildReport(in reportInput) Report {
	level := levelOK
	raise := func(l int) {
		if l > level {
			level = l
		}
	}
	var headline string
	var done, notDone, other []string

	byKind := map[string][]*Group{}
	for _, g := range in.Groups {
		byKind[g.Kind] = append(byKind[g.Kind], g)
	}
	for _, gs := range byKind {
		sort.Slice(gs, func(i, j int) bool { return gs[i].First.Before(gs[j].First) })
	}

	var info []string
	var newest time.Time
	if in.State == nil {
		raise(levelBad)
		headline = "backup state unknown"
		notDone = append(notDone, fmt.Sprintf("Time Machine preferences could not be read: %v", in.StateErr))
	} else {
		for _, d := range in.State.Dest {
			for _, s := range d.Snapshots {
				if s.After(newest) {
					newest = s
				}
				if s.After(in.From) && !s.After(in.To) {
					done = append(done, fmt.Sprintf("%s  backup completed on %q", stamp(s), d.Name))
				}
			}
			for _, a := range unfinished(d, in.From, in.To, in.State.Running) {
				raise(levelWarn)
				notDone = append(notDone, fmt.Sprintf("%s  backup started on %q but did not complete", stamp(a), d.Name))
			}
			l := fmt.Sprintf("%q: %s free, %s used", d.Name, gb(d.Free), gb(d.Used))
			if n := len(d.Snapshots); n > 0 {
				l += ", last completed " + stamp(d.Snapshots[n-1])
			}
			info = append(info, l)
		}
		switch {
		case len(in.State.Dest) == 0:
			raise(levelBad)
			headline = "no backup disk configured"
		case newest.IsZero():
			raise(levelBad)
			headline = "no completed backup on record"
		default:
			days := in.To.Sub(newest).Hours() / 24
			if days >= in.BadDays {
				raise(levelBad)
			} else if days >= in.WarnDays {
				raise(levelWarn)
			}
			headline = "last backup " + age(in.To.Sub(newest)) + " ago"
		}
		if !in.State.AutoBackup {
			raise(levelWarn)
			notDone = append(notDone, "Automatic backups are switched off")
		}
		if in.State.RunningKnown && in.State.Running {
			done = append(done, "A backup is running right now")
		}
		s := "Automatic backups: every " + age(in.State.Interval)
		if in.State.Interval == 0 {
			s = "Automatic backups: interval not set"
		}
		if in.State.RequiresAC {
			s += ", only on AC power"
		}
		info = append(info, s)
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
	if len(byKind[kindProblem]) > 0 {
		raise(levelWarn)
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
		notDone = append(notDone, fmt.Sprintf("The system log could not be read: %v", in.LogErr))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Time Machine on %s: %s, %s\n", in.Host, levelName[level], headline)
	fmt.Fprintf(&b, "Period: %s to %s\n", stamp(in.From), stamp(in.To))
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
	section("Done", done, "no backup completed in this period")
	section("Not done", notDone, "nothing to report")
	section("Settings and disks", info, "")
	section("Other log messages", other, "")
	return Report{
		Subject: fmt.Sprintf("[TmWell] %s %s: %s", in.Host, levelName[level], headline),
		Body:    b.String(),
	}
}
