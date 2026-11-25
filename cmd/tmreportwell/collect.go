package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

const tmPlist = "/Library/Preferences/com.apple.TimeMachine.plist"

// Destination is one backup disk as recorded in the Time Machine preferences.
type Destination struct {
	Name      string
	UUIDs     []string
	Snapshots []time.Time // completed backups, ascending
	Attempts  []time.Time // started backups, ascending
	Free      int64
	Used      int64
	Attached  *bool // nil when it could not be determined
}

// TMState is what the Time Machine preferences say right now.
type TMState struct {
	AutoBackup   bool
	Interval     time.Duration
	RequiresAC   bool
	Running      bool
	RunningKnown bool
	Dest         []Destination
	OnBattery    *bool // nil when it could not be determined
	LocalSnaps   []time.Time
}

func run(timeout time.Duration, name string, arg ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var errb bytes.Buffer
	c := exec.CommandContext(ctx, name, arg...)
	c.Stderr = &errb
	out, err := c.Output()
	if err != nil {
		msg := strings.TrimSpace(errb.String() + " " + string(out))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("%s: %w: %s", name, err, msg)
	}
	return out, nil
}

func readTMState() (*TMState, error) {
	b, err := run(30*time.Second, "/usr/bin/plutil", "-convert", "xml1", "-o", "-", tmPlist)
	if err != nil {
		return nil, err
	}
	st, err := decodeTMState(b)
	if err != nil {
		return nil, err
	}
	if b, err := run(30*time.Second, "/usr/bin/tmutil", "status", "-X"); err == nil {
		if m, err := parsePlist(b); err == nil {
			st.Running, st.RunningKnown = m["Running"].(bool)
		}
	}
	if b, err := run(30*time.Second, "/usr/bin/pmset", "-g", "batt"); err == nil {
		bat := strings.Contains(string(b), "'Battery Power'")
		if bat || strings.Contains(string(b), "'AC Power'") {
			st.OnBattery = &bat
		}
	}
	if b, err := run(30*time.Second, "/usr/bin/tmutil", "listlocalsnapshots", "/"); err == nil {
		st.LocalSnaps = parseLocalSnaps(string(b))
	}
	for i := range st.Dest {
		st.Dest[i].Attached = attached(st.Dest[i].UUIDs)
	}
	return st, nil
}

// attached asks diskutil whether a volume of the destination is present.
// diskutil exits 1 with "Could not find disk" for an unplugged one; any other
// failure says nothing about the disk.
func attached(ids []string) *bool {
	if len(ids) == 0 {
		return nil
	}
	found := false
	for _, u := range ids {
		_, err := run(30*time.Second, "/usr/sbin/diskutil", "info", "-plist", u)
		if err == nil {
			found = true
		} else if !strings.Contains(err.Error(), "Could not find disk") {
			return nil
		}
	}
	return &found
}

var localSnap = regexp.MustCompile(`(?m)^com\.apple\.TimeMachine\.(\d{4}-\d{2}-\d{2}-\d{6})\.local$`)

func parseLocalSnaps(out string) []time.Time {
	var r []time.Time
	for _, m := range localSnap.FindAllStringSubmatch(out, -1) {
		if t, err := time.ParseInLocation("2006-01-02-150405", m[1], time.Local); err == nil {
			r = append(r, t)
		}
	}
	sort.Slice(r, func(i, j int) bool { return r[i].Before(r[j]) })
	return r
}

func decodeTMState(b []byte) (*TMState, error) {
	m, err := parsePlist(b)
	if err != nil {
		return nil, err
	}
	st := &TMState{AutoBackup: truthy(m["AutoBackup"]), RequiresAC: truthy(m["RequiresACPower"])}
	if n, ok := m["AutoBackupInterval"].(int64); ok {
		st.Interval = time.Duration(n) * time.Second
	}
	dests, _ := m["Destinations"].([]any)
	for _, x := range dests {
		dm, ok := x.(map[string]any)
		if !ok {
			continue
		}
		d := Destination{Snapshots: dates(dm["SnapshotDates"]), Attempts: dates(dm["AttemptDates"])}
		d.Name, _ = dm["LastKnownVolumeName"].(string)
		if d.Name == "" {
			d.Name, _ = dm["DestinationID"].(string)
		}
		d.Free, _ = dm["BytesAvailable"].(int64)
		d.Used, _ = dm["BytesUsed"].(int64)
		uu, _ := dm["DestinationUUIDs"].([]any)
		for _, u := range uu {
			if s, ok := u.(string); ok {
				d.UUIDs = append(d.UUIDs, s)
			}
		}
		st.Dest = append(st.Dest, d)
	}
	return st, nil
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case int64:
		return x != 0
	}
	return false
}

func dates(v any) []time.Time {
	var r []time.Time
	a, _ := v.([]any)
	for _, x := range a {
		if t, ok := x.(time.Time); ok {
			r = append(r, t)
		}
	}
	sort.Slice(r, func(i, j int) bool { return r[i].Before(r[j]) })
	return r
}

// Event kinds.
const (
	kindSkip    = "skip"    // a due backup was not run
	kindNotDue  = "notdue"  // the scheduler looked and found nothing to do
	kindNoDisk  = "nodisk"  // the backup disk was not mounted
	kindProblem = "problem" // any other error or fault
	kindOther   = "other"   // anything not recognised, shown verbatim
)

// Group collects log messages that differ only in numbers.
type Group struct {
	Kind  string    `json:"kind"`
	Text  string    `json:"text"`
	Count int       `json:"count"`
	First time.Time `json:"first"`
	Last  time.Time `json:"last"`
}

var (
	digits = regexp.MustCompile(`0x[0-9a-fA-F]+|[0-9]+`)
	spaces = regexp.MustCompile(`\s+`)
	uuids  = regexp.MustCompile(`[0-9A-F]{8}(?:-[0-9A-F]{4}){3}-[0-9A-F]{12}`)
	// Chatter that carries no information about backups. All of these were
	// seen in bulk on macOS 27.2 on a day without any backup.
	noise = []*regexp.Regexp{
		regexp.MustCompile(`: connection invalid$`),
		regexp.MustCompile(`^Received notifyd event `),
		regexp.MustCompile(`^TMPowerState: `),
		regexp.MustCompile(`^Failed to send \S+ analytics`),
		regexp.MustCompile(`^Failed to read capabilities for '`),
		regexp.MustCompile(`does not support SMB FullFSync$`),
		regexp.MustCompile(`^Mounting destination \S+ for browsing`),
	}
)

// The scheduler names the attempt it counts from, also when that attempt was
// skipped, as "since last auto backup attempt at 2026-11-25 10:05:18 +0000".
var lastAttempt = regexp.MustCompile(`last auto backup attempt at (\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{4})`)

const notDueText = "backup not due yet"

const skipPrefix = "Skipping scheduled Time Machine backup: "

func classify(level, msg string) (kind, text string, keep bool) {
	text = spaces.ReplaceAllString(strings.TrimSpace(msg), " ")
	for _, r := range noise {
		if r.MatchString(text) {
			return "", "", false
		}
	}
	if reason, ok := strings.CutPrefix(text, skipPrefix); ok {
		if strings.HasPrefix(reason, "Autobackup interval is") {
			if m := lastAttempt.FindStringSubmatch(reason); m != nil {
				return kindNotDue, notDueText + ", last attempt " + m[1], true
			}
			return kindNotDue, notDueText, true
		}
		return kindSkip, reason, true
	}
	if strings.HasPrefix(text, "Failed to find any mounted disk matching volume UUIDs") {
		return kindNoDisk, "backup disk not mounted " + uuids.FindString(text), true
	}
	if level == "Error" || level == "Fault" {
		return kindProblem, text, true
	}
	return kindOther, text, true
}

func groupKey(kind, text string) string {
	return kind + "\x00" + digits.ReplaceAllString(text, "#")
}

func addEvent(groups map[string]*Group, kind, text string, t time.Time) {
	k := groupKey(kind, text)
	g := groups[k]
	if g == nil {
		g = &Group{Kind: kind, First: t}
		groups[k] = g
	}
	g.Count++
	if t.Before(g.First) {
		g.First = t
	}
	if !t.Before(g.Last) {
		g.Last, g.Text = t, text
	}
}

const logTime = "2006-01-02 15:04:05.000000-0700"

// parseLog adds every event newer than after to groups and returns the newest
// timestamp seen. The ndjson stream ends in a trailer without a timestamp.
func parseLog(r io.Reader, after time.Time, groups map[string]*Group) (time.Time, error) {
	newest := after
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var e struct {
			Timestamp    string `json:"timestamp"`
			MessageType  string `json:"messageType"`
			EventMessage string `json:"eventMessage"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Timestamp == "" {
			continue
		}
		t, err := time.Parse(logTime, e.Timestamp)
		if err != nil || !t.After(after) {
			continue
		}
		if t.After(newest) {
			newest = t
		}
		if kind, text, keep := classify(e.MessageType, e.EventMessage); keep {
			addEvent(groups, kind, text, t)
		}
	}
	return newest, sc.Err()
}

func collectLog(after time.Time, groups map[string]*Group) (time.Time, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, "/usr/bin/log", "show", "--info", "--style", "ndjson",
		"--start", after.Local().Format("2006-01-02 15:04:05"), "--predicate",
		`subsystem == "com.apple.TimeMachine" AND (process == "backupd" OR process == "backupd-helper")`)
	var errb bytes.Buffer
	c.Stderr = &errb
	out, err := c.StdoutPipe()
	if err != nil {
		return after, err
	}
	if err := c.Start(); err != nil {
		return after, err
	}
	// Parse into a scratch map: a failed run is repeated from the same cursor
	// and would otherwise count its events twice.
	tmp := map[string]*Group{}
	newest, perr := parseLog(out, after, tmp)
	if perr != nil {
		cancel()
	}
	if err := c.Wait(); err != nil {
		return after, fmt.Errorf("log show: %w: %s", err, strings.TrimSpace(errb.String()))
	}
	if perr != nil {
		return after, perr
	}
	for k, n := range tmp {
		g := groups[k]
		if g == nil {
			groups[k] = n
			continue
		}
		g.Count += n.Count
		g.Last, g.Text = n.Last, n.Text
	}
	return newest, nil
}

func hostname() string {
	if b, err := run(5*time.Second, "/usr/sbin/scutil", "--get", "ComputerName"); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s
		}
	}
	h, _ := os.Hostname()
	return h
}
