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
	if got := unfinished(d, at("2026-09-01 00:00"), at("2026-12-01 00:00"), false); len(got) != 2 {
		t.Errorf("unfinished = %v, want the two September attempts", got)
	}
	d.Snapshots = nil
	if got := unfinished(d, at("2026-10-01 00:00"), at("2026-12-01 00:00"), true); len(got) != 0 {
		t.Errorf("running backup reported as unfinished: %v", got)
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
	parseLog(strings.NewReader(in), newest, g)
	if g[groupKey(kindSkip, "Computer is on battery power")].Count != 2 {
		t.Error("events counted twice")
	}
}

func TestReportLevels(t *testing.T) {
	now := at("2026-11-25 09:00")
	mk := func(last time.Time) Report {
		return buildReport(reportInput{Host: "m", From: now.Add(-24 * time.Hour), To: now, WarnDays: 2, BadDays: 7,
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
