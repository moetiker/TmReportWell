package main

import (
	"io"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.Local)
	if err != nil {
		panic(err)
	}
	return t
}

const prefs = `<?xml version="1.0"?><!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "x">
<plist version="1.0"><dict>
<key>AutoBackup</key><integer>1</integer><key>AutoBackupInterval</key><integer>86400</integer>
<key>Destinations</key><array><dict>
 <key>AttemptDates</key><array><date>2026-09-06T15:12:57Z</date><date>2026-09-06T15:20:14Z</date><date>2026-10-11T15:39:04Z</date></array>
 <key>DestinationUUIDs</key><array><string>00000000-1111-2222-3333-444444444444</string></array>
 <key>LastKnownVolumeName</key><string>Disk</string><key>Empty</key><array/>
 <key>SnapshotDates</key><array><date>2026-10-11T16:42:36Z</date></array>
</dict></array><key>RequiresACPower</key><true/></dict></plist>`

func TestDecodeAndUnfinished(t *testing.T) {
	st, err := decodeTMState([]byte(prefs))
	if err != nil {
		t.Fatal(err)
	}
	if !st.AutoBackup || !st.RequiresAC || st.Interval != 24*time.Hour || len(st.Dest) != 1 {
		t.Fatalf("%+v", st)
	}
	d := st.Dest[0]
	if got := unfinished(d, at("2026-09-01 00:00"), at("2026-12-01 00:00"), time.Time{}); len(got) != 2 {
		t.Errorf("unfinished = %v, want the two September attempts", got)
	}
}

func TestParseLog(t *testing.T) {
	in := `{"timestamp":"2026-11-24 11:07:07.000001+0100","messageType":"Info","eventMessage":"Skipping scheduled Time Machine backup: Computer is on battery power"}
{"timestamp":"2026-11-25 11:05:48.000000+0100","messageType":"Info","eventMessage":"Skipping scheduled Time Machine backup: Computer is on battery power"}
{"timestamp":"2026-11-25 11:35:22.000000+0100","messageType":"Info","eventMessage":"Skipping scheduled Time Machine backup: Autobackup interval is 24 hours but only 0.5 hours have elapsed"}
{"timestamp":"2026-11-25 11:36:00.000000+0100","messageType":"Error","eventMessage":"com.apple.backupd.xpc: connection invalid"}
{"timestamp":"2026-11-25 11:53:53.000000+0100","messageType":"Info","eventMessage":"Failed to find any mounted disk matching volume UUIDs: {(\n    \"00000000-1111-2222-3333-444444444444\"\n)}"}
{"count":5,"finished":1}
`
	g := map[string]*Group{}
	start := at("2026-11-24 00:00")
	newest, err := parseLog(strings.NewReader(in), start, g)
	if err != nil {
		t.Fatal(err)
	}
	if len(g) != 3 || g[groupKey(kindSkip, "Computer is on battery power")].Count != 2 {
		t.Fatalf("%+v", g)
	}
	// Reading the same stream again from the cursor adds nothing.
	if _, err := parseLog(strings.NewReader(in), newest, g); err != nil {
		t.Fatal(err)
	}
	if g[groupKey(kindSkip, "Computer is on battery power")].Count != 2 {
		t.Error("events counted twice")
	}
}

func TestReportLevels(t *testing.T) {
	now := at("2026-11-25 09:00")
	mk := func(last time.Time) Report {
		return buildReport(reportInput{Host: "m", From: now.Add(-24 * time.Hour), PrefsFrom: now.Add(-24 * time.Hour), To: now, WarnDays: 2, BadDays: 7,
			State: &TMState{AutoBackup: true, Interval: 24 * time.Hour, Dest: []Destination{{Name: "D", Snapshots: []time.Time{last}}}}})
	}
	for last, want := range map[string]string{"2026-11-25 03:00": " OK:", "2026-11-22 09:00": " WARNING:", "2026-11-08 09:00": " PROBLEM:"} {
		if r := mk(at(last)); !strings.Contains(r.Subject, want) {
			t.Errorf("last %s: %s", last, r.Subject)
		}
	}
	if r := mk(at("2026-11-25 03:00")); !strings.Contains(r.Body, "backup completed on \"D\"") {
		t.Error(r.Body)
	}
	r := buildReport(reportInput{Host: "m", From: now, To: now, StateErr: io.ErrUnexpectedEOF})
	if !strings.Contains(r.Subject, "PROBLEM: backup state unknown") {
		t.Error(r.Subject)
	}
}

func TestDue(t *testing.T) {
	now := at("2026-11-25 12:00")
	if !due("09:00", at("2026-11-24 23:00"), now) || due("09:00", at("2026-11-25 09:10"), now) || due("13:00", at("2026-11-24 13:00"), now) {
		t.Error("due")
	}
	// Sent by hand at 08:00: no second mail at 09:00, the next one tomorrow.
	if due("09:00", at("2026-11-25 08:00"), now) || !due("09:00", at("2026-11-25 08:00"), at("2026-11-26 09:00")) {
		t.Error("more or less than one mail per day")
	}
}

func TestComposeMail(t *testing.T) {
	b, err := composeMail("a@example.com", "b@example.com", Report{"Zürich", "grün\n"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	m, err := mail.ReadMessage(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(quotedprintable.NewReader(m.Body))
	if !strings.Contains(string(body), "grün") {
		t.Errorf("%q", body)
	}
}

func ptr(b bool) *bool { return &b }

// A backup that runs while the mail goes out and fails afterwards shows up in
// the next report.
func TestAbortAfterReport(t *testing.T) {
	start, first, second := at("2026-11-24 08:30"), at("2026-11-24 09:00"), at("2026-11-25 09:00")
	st := &TMState{AutoBackup: true, Running: true, RunningKnown: true, Dest: []Destination{
		{Name: "A", Attempts: []time.Time{start}},
		{Name: "B", Attempts: []time.Time{at("2026-11-24 07:00")}},
	}}
	s := &State{LastSent: at("2026-11-23 09:00"), PrefsFrom: at("2026-11-23 09:00"), AttemptsFrom: at("2026-11-23 09:00")}
	rep := func(now time.Time) string {
		return buildReport(reportInput{From: s.LastSent, To: now, PrefsFrom: s.PrefsFrom, AttemptsFrom: s.AttemptsFrom,
			State: st, WarnDays: 2, BadDays: 7}).Body
	}
	body := rep(first)
	if strings.Contains(body, `on "A" did not`) || !strings.Contains(body, `on "B" did not complete`) {
		t.Errorf("only B has failed so far:\n%s", body)
	}
	s.markSent(first, st)
	st.Running = false
	body = rep(second)
	if !strings.Contains(body, `on "A" did not complete`) || strings.Contains(body, `on "B" did not`) {
		t.Errorf("A must be reported now, B not again:\n%s", body)
	}
	s.markSent(second, st)
	if body = rep(at("2026-11-26 09:00")); strings.Contains(body, "did not complete") {
		t.Errorf("reported twice:\n%s", body)
	}
}

// Unreadable preferences leave the period open for backups.
func TestMarkSentWithoutPrefs(t *testing.T) {
	old := at("2026-11-23 09:00")
	s := &State{PrefsFrom: old, AttemptsFrom: old, Gaps: []Gap{{old, old}}}
	s.markSent(at("2026-11-24 09:00"), nil)
	if !s.PrefsFrom.Equal(old) || !s.AttemptsFrom.Equal(old) || s.Gaps != nil {
		t.Errorf("%+v", s)
	}
}

func TestLogGap(t *testing.T) {
	s := &State{}
	s.noteGap(at("2026-11-20 09:00"))
	s.noteGap(at("2026-11-21 08:00"))
	if len(s.Gaps) != 0 {
		t.Fatalf("unexpected gap: %v", s.Gaps)
	}
	s.noteGap(at("2026-11-23 10:00"))
	if len(s.Gaps) != 1 || !s.Gaps[0].To.Equal(at("2026-11-22 10:00")) {
		t.Fatalf("%v", s.Gaps)
	}
	now := at("2026-11-23 11:00")
	r := buildReport(reportInput{To: now, Gaps: s.Gaps, WarnDays: 2, BadDays: 7, State: &TMState{AutoBackup: true, RunningKnown: true,
		Dest: []Destination{{Name: "D", Snapshots: []time.Time{now}}}}})
	if !strings.Contains(r.Body, "may be missing") || !strings.Contains(r.Subject, " OK:") {
		t.Errorf("%s\n%s", r.Subject, r.Body)
	}
}

func TestStaleDiskNotHidden(t *testing.T) {
	now := at("2026-11-25 09:00")
	r := buildReport(reportInput{Host: "m", To: now, WarnDays: 2, BadDays: 7, State: &TMState{AutoBackup: true, RunningKnown: true,
		Dest: []Destination{{Name: "New", Snapshots: []time.Time{now}}, {Name: "Old", Snapshots: []time.Time{at("2026-10-01 09:00")}}}}})
	if !strings.Contains(r.Subject, `PROBLEM: "Old" last backup 55 days ago`) {
		t.Error(r.Subject)
	}
}

func TestSituation(t *testing.T) {
	now := at("2026-11-25 13:00")
	groups := map[string]*Group{}
	for _, e := range []struct{ when, msg string }{
		{"2026-11-25 11:05:48.945000+0100", "Computer is on battery power"},
		{"2026-11-25 11:35:40.000000+0100", "Autobackup interval is 24 hours but only 0.5 hours have elapsed since last auto backup attempt at 2026-11-25 10:05:18 +0000"},
	} {
		ts, err := time.Parse(logTime, e.when)
		if err != nil {
			t.Fatal(err)
		}
		kind, text, _ := classify("Info", skipPrefix+e.msg)
		addEvent(groups, kind, text, ts)
	}
	st := &TMState{AutoBackup: true, RequiresAC: true, Interval: 24 * time.Hour, RunningKnown: true, OnBattery: ptr(true),
		Dest: []Destination{{Name: "D", Attached: ptr(false), Snapshots: []time.Time{at("2026-11-08 13:32")}}}}
	in := reportInput{From: now.Add(-24 * time.Hour), To: now, Groups: groups, State: st, WarnDays: 2, BadDays: 7}
	body := buildReport(in).Body
	next := time.Date(2026, 11, 26, 10, 5, 18, 0, time.UTC)
	for _, want := range []string{
		`"D" is not connected`, "runs on battery", "was skipped (Computer is on battery power)",
		"does not try again by itself before " + stamp(next),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
	for _, gone := range []string{"What to do", "Not done", "Backups were skipped"} {
		if strings.Contains(body, gone) {
			t.Errorf("%q does not belong in\n%s", gone, body)
		}
	}
	// A skip that the next attempt no longer explains is still told.
	in.To = at("2026-11-26 13:00")
	if body = buildReport(in).Body; !strings.Contains(body, "Backups were skipped: Computer is on battery power") {
		t.Error(body)
	}
	in.To = now
	// No claim about unknown facts.
	st.Dest[0].Snapshots, st.Dest[0].Attached, st.OnBattery = []time.Time{now}, nil, nil
	body = buildReport(in).Body
	if strings.Contains(body, "not connected") || strings.Contains(body, "on battery, and") {
		t.Error(body)
	}
}

func TestLocalSnaps(t *testing.T) {
	got := parseLocalSnaps("Snapshots for disk /:\ncom.apple.TimeMachine.2026-11-21-100551.local\ncom.apple.TimeMachine.2026-11-08-104558.local\n")
	if len(got) != 2 || got[0].Day() != 8 {
		t.Error(got)
	}
}
