package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Report is the rendered mail.
type Report struct{ Subject, Body, HTML string }

// statement is one sentence of the situation. Its level gives it a colour.
type statement struct {
	Level int
	Text  string
}

// diskView is what the HTML report shows about one disk.
type diskView struct {
	Name, Age, When string
	Level           int
	Connection      string // empty when unknown
	Connected       bool
	Free, Used      string
	Percent         int // used share of the disk, -1 when unknown
}

const (
	levelOK = iota
	levelWarn
	levelBad
	levelNone // neither good nor bad
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
	var done, info, diskInfo, other []string
	var situation []statement
	var disks []diskView
	say := func(l int, format string, a ...any) {
		situation = append(situation, statement{l, fmt.Sprintf(format, a...)})
	}

	byKind := map[string][]*Group{}
	for _, g := range in.Groups {
		byKind[g.Kind] = append(byKind[g.Kind], g)
	}
	for _, gs := range byKind {
		sort.Slice(gs, func(i, j int) bool { return gs[i].First.Before(gs[j].First) })
	}

	told := "" // the skip reason the situation already names
	if in.State == nil {
		raise(levelBad)
		headline = "backup state unknown"
		say(levelBad, "It is not known whether backups work: the Time Machine preferences could not be read (%v). tmreportwell may need Full Disk Access in System Settings, Privacy & Security.", in.StateErr)
	} else {
		st := in.State
		open, isOpen := openAttempt(st, in.AttemptsFrom, in.To)
		running := st.RunningKnown && st.Running
		worst, completed := -1.0, 0
		for _, d := range st.Dest {
			for _, s := range d.Snapshots {
				if s.After(in.PrefsFrom) && !s.After(in.To) {
					completed++
					done = append(done, fmt.Sprintf("%s  backup completed on %q", stamp(s), d.Name))
				}
			}
			for _, a := range unfinished(d, in.AttemptsFrom, in.To, open) {
				raise(levelWarn)
				say(levelWarn, "The backup started %s on %q did not complete.", stamp(a), d.Name)
			}
			diskInfo = append(diskInfo, fmt.Sprintf("%q: %s free, %s used", d.Name, gb(d.Free), gb(d.Used)))

			// Every disk is judged on its own: a fresh one must not hide a stale one.
			days, text, diskLevel := 1e9, "no completed backup", levelOK
			if n := len(d.Snapshots); n > 0 {
				days = in.To.Sub(d.Snapshots[n-1]).Hours() / 24
			}
			if days >= in.BadDays {
				diskLevel = levelBad
			} else if days >= in.WarnDays {
				diskLevel = levelWarn
			}
			raise(diskLevel)
			v := diskView{Name: d.Name, Age: "never", Level: diskLevel, Free: gb(d.Free), Used: gb(d.Used), Percent: -1}
			if total := d.Free + d.Used; total > 0 {
				v.Percent = int(float64(d.Used) / float64(total) * 100)
			}
			if n := len(d.Snapshots); n > 0 {
				last := d.Snapshots[n-1]
				v.Age, v.When = age(in.To.Sub(last))+" ago", stamp(last)
				text = "last backup " + v.Age
				say(diskLevel, "The last backup on %q completed %s, %s.", d.Name, v.When, v.Age)
			} else {
				say(levelBad, "%q has no completed backup on record.", d.Name)
			}
			if days > worst {
				worst, headline = days, text
				if len(st.Dest) > 1 {
					headline = fmt.Sprintf("%q %s", d.Name, text)
				}
			}
			if d.Attached != nil {
				v.Connected, v.Connection = *d.Attached, "connected"
			}
			if no(d.Attached) {
				v.Connection = "not connected"
				connLevel := levelNone
				if diskLevel != levelOK {
					connLevel = levelWarn
				}
				say(connLevel, "%q is not connected at the time of this report.", d.Name)
			}
			disks = append(disks, v)
		}
		if len(st.Dest) == 0 {
			raise(levelBad)
			headline = "no backup disk configured"
			say(levelBad, "No backup disk is configured.")
		} else if completed == 0 && !running {
			say(levelNone, "No backup completed since the last report.")
		}
		switch {
		case running:
			say(levelOK, "A backup is running right now.")
		case isOpen:
			say(levelNone, "The outcome of the backup started %s is not known yet.", stamp(open))
		}
		if !st.AutoBackup {
			raise(levelWarn)
			say(levelWarn, "Automatic backups are switched off.")
		}
		if st.RequiresAC && yes(st.OnBattery) {
			say(levelNone, "The Mac runs on battery, and automatic backups need AC power.")
		}
		if attempt, next, skipped := nextAttempt(st, byKind[kindNotDue], byKind[kindSkip]); st.AutoBackup && !running && next.After(in.To) {
			s := fmt.Sprintf("macOS does not try again by itself before %s.", stamp(next))
			if skipped != "" {
				told = skipped
				s = fmt.Sprintf("The attempt of %s was skipped (%s). %s", stamp(attempt), skipped, s)
			}
			say(levelNone, "%s", s)
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
		if g.Text != told {
			say(levelWarn, "Backups were skipped: %s (%s).", g.Text, span(g))
		}
	}
	if n := len(byKind[kindProblem]); n > 0 {
		raise(levelWarn)
		say(levelWarn, "Time Machine logged errors, see the log messages below.")
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
		say(levelWarn, "The system log could not be read, so reasons for skipped backups may be missing: %v", in.LogErr)
	}
	for _, g := range in.Gaps {
		say(levelNone, "Log messages from %s to %s may be missing: tmreportwell did not run, and macOS keeps them for about a day.", stamp(g.From), stamp(g.To))
	}

	p := page{Host: in.Host, Level: level, LevelName: levelName[level], Headline: headline,
		From: stamp(in.From), To: stamp(in.To), Situation: situation, Disks: disks, Done: done, Settings: info, Log: other}
	return Report{
		Subject: fmt.Sprintf("%s: %s %s, %s", reportTitle, in.Host, levelName[level], headline),
		Body:    renderText(p, diskInfo),
		HTML:    renderHTML(p),
	}
}

// page is the content both renderings show.
type page struct {
	Host, LevelName, Headline string
	Level                     int
	From, To                  string
	Situation                 []statement
	Disks                     []diskView
	Done, Settings, Log       []string
}

const (
	reportTitle = "Timemachine Report"
	homeURL     = "https://github.com/moetiker/TmReportWell"
)

// origin says what sent the mail. A build without a release version names none.
func origin() string {
	s := "Sent by TmReportWell"
	if version != "0.0.0" {
		s += " " + version
	}
	return s + ", free software by Manuel Oetiker (MIT licence)"
}

const nothingDone = "no backup completed in this period"

func renderText(p page, diskInfo []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n%s: %s, %s\n", reportTitle, p.Host, p.LevelName, p.Headline)
	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n%s\n", title)
		for _, l := range lines {
			fmt.Fprintf(&b, "  - %s\n", l)
		}
	}
	var sit []string
	for _, s := range p.Situation {
		sit = append(sit, s.Text)
	}
	section("Situation", sit)
	fmt.Fprintf(&b, "\nDetails for %s to %s\n", p.From, p.To)
	done := p.Done
	if len(done) == 0 {
		done = []string{nothingDone}
	}
	section("Done", done)
	section("Settings and disks", append(diskInfo, p.Settings...))
	section("Other log messages", p.Log)
	fmt.Fprintf(&b, "\n-- \n%s\n%s\n", origin(), homeURL)
	return b.String()
}
