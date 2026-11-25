// tmreportwell mails a daily report about what Time Machine did and did not do.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const agentLabel = "tmreportwell"

// Config is read from config.json.
type Config struct {
	SMTPHost string  `json:"smtp_host"`
	SMTPPort int     `json:"smtp_port"`
	SMTPUser string  `json:"smtp_user"`
	From     string  `json:"from"`
	To       string  `json:"to"`
	SendAt   string  `json:"send_at"`
	WarnDays float64 `json:"warn_days"`
	BadDays  float64 `json:"problem_days"`
}

// logRetention is how long macOS kept Time Machine messages on macOS 27.2.
const logRetention = 24 * time.Hour

// Gap is a time span whose log messages may be lost.
type Gap struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// State survives between the hourly runs.
type State struct {
	Cursor       time.Time         `json:"cursor"`        // newest log event already collected
	Collected    time.Time         `json:"collected"`     // last successful collection
	LastSent     time.Time         `json:"last_sent"`     // end of the last mailed period
	PrefsFrom    time.Time         `json:"prefs_from"`    // completed backups after this are unreported
	AttemptsFrom time.Time         `json:"attempts_from"` // started backups after this are unresolved
	Gaps         []Gap             `json:"gaps,omitempty"`
	Groups       map[string]*Group `json:"groups"`
}

// noteGap records a span the collector slept through for longer than macOS
// keeps the messages.
func (s *State) noteGap(now time.Time) {
	if !s.Collected.IsZero() && now.Sub(s.Collected) > logRetention {
		s.Gaps = append(s.Gaps, Gap{s.Collected, now.Add(-logRetention)})
	}
	s.Collected = now
}

func (s *State) collect(now time.Time) error {
	newest, err := collectLog(s.Cursor, s.Groups)
	if err != nil {
		return err
	}
	s.Cursor = newest
	s.noteGap(now)
	return nil
}

// markSent starts the next period. What the sent report could not settle stays
// open: everything when the preferences were unreadable, and an attempt that
// was still running.
func (s *State) markSent(now time.Time, st *TMState) {
	if st != nil {
		open, ok := openAttempt(st, s.AttemptsFrom, now)
		s.PrefsFrom, s.AttemptsFrom = now, now
		if ok {
			s.AttemptsFrom = open.Add(-time.Second)
		}
	}
	s.LastSent, s.Groups, s.Gaps = now, map[string]*Group{}, nil
}

func dir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		fatal(err)
	}
	return filepath.Join(h, "Library", "Application Support", "TmReportWell")
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "%s tmreportwell: %v\n", time.Now().Format(time.DateTime), err)
	os.Exit(1)
}

func loadConfig() (*Config, error) {
	c := &Config{SMTPPort: 587, SendAt: "09:00", WarnDays: 2, BadDays: 7}
	b, err := os.ReadFile(filepath.Join(dir(), "config.json"))
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return c, fmt.Errorf("config.json: %w", err)
	}
	if _, err := time.Parse("15:04", c.SendAt); err != nil {
		return c, fmt.Errorf("config.json: send_at must look like 09:00, not %q", c.SendAt)
	}
	for k, v := range map[string]string{"smtp_host": c.SMTPHost, "smtp_user": c.SMTPUser, "from": c.From, "to": c.To} {
		if v == "" {
			return c, fmt.Errorf("config.json: %s is missing", k)
		}
	}
	return c, nil
}

func loadState(now time.Time) (*State, error) {
	s := &State{}
	b, err := os.ReadFile(filepath.Join(dir(), "state.json"))
	if err == nil {
		err = json.Unmarshal(b, s)
	} else if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if s.Groups == nil {
		s.Groups = map[string]*Group{}
	}
	if s.Cursor.IsZero() {
		s.Cursor = now.Add(-24 * time.Hour)
	}
	if s.LastSent.IsZero() {
		s.LastSent = now.Add(-24 * time.Hour)
	}
	if s.PrefsFrom.IsZero() {
		s.PrefsFrom = s.LastSent
	}
	if s.AttemptsFrom.IsZero() {
		s.AttemptsFrom = s.LastSent
	}
	return s, err
}

// lock keeps a second tmreportwell waiting. Without it a send by hand during
// the hourly run mails the same report twice.
func lock() (*os.File, error) {
	if err := os.MkdirAll(dir(), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir(), "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func saveState(s *State) error {
	b, err := json.MarshalIndent(s, "", " ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir(), 0o700); err != nil {
		return err
	}
	p := filepath.Join(dir(), "state.json")
	if err := os.WriteFile(p+".new", b, 0o600); err != nil {
		return err
	}
	return os.Rename(p+".new", p)
}

// due reports whether today's mail is still to be sent. A mail sent earlier
// today, also by hand before send_at, is today's mail.
func due(sendAt string, lastSent, now time.Time) bool {
	t, err := time.Parse("15:04", sendAt)
	if err != nil {
		return false
	}
	slot := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	y, m, d := lastSent.In(now.Location()).Date()
	sentToday := y == now.Year() && m == now.Month() && d == now.Day()
	return !now.Before(slot) && lastSent.Before(slot) && !sentToday
}

func report(c *Config, s *State, now time.Time, logErr error) (Report, *TMState) {
	st, err := readTMState()
	return buildReport(reportInput{Host: hostname(), From: s.LastSent, To: now, PrefsFrom: s.PrefsFrom,
		AttemptsFrom: s.AttemptsFrom, State: st, StateErr: err, LogErr: logErr, Gaps: s.Gaps, Groups: s.Groups,
		WarnDays: c.WarnDays, BadDays: c.BadDays}), st
}

func cmdReport() error {
	c, err := loadConfig()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	now := time.Now()
	s, err := loadState(now)
	if err != nil {
		return err
	}
	logErr := s.collect(now)
	r, _ := report(c, s, now, logErr)
	fmt.Printf("Subject: %s\n\n%s", r.Subject, r.Body)
	return nil
}

func cmdRun(force bool) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	l, err := lock()
	if err != nil {
		return err
	}
	defer func() { _ = l.Close() }()
	now := time.Now()
	s, err := loadState(now)
	if err != nil {
		return err
	}
	logErr := s.collect(now)
	// Saved before mailing: the log drops these messages after about a day, so
	// they must outlive a mail server that stays unreachable for longer.
	if err := saveState(s); err != nil {
		return err
	}
	if !force && !due(c.SendAt, s.LastSent, now) {
		return logErr
	}
	r, st := report(c, s, now, logErr)
	if err := sendMail(c, r); err != nil {
		return fmt.Errorf("sending mail: %w", err)
	}
	s.markSent(now, st)
	return saveState(s)
}

func agentPath() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, "Library", "LaunchAgents", agentLabel+".plist")
}

func launchDomain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func cmdInstall() error {
	if _, err := loadConfig(); err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Symlinks stay unresolved: brew links bin/ into a Cellar directory named
	// after the version, which the next upgrade removes.
	h, _ := os.UserHomeDir()
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	pl := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key><array><string>%s</string><string>run</string></array>
	<key>RunAtLoad</key><true/>
	<key>StartInterval</key><integer>3600</integer>
	<key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, agentLabel, esc.Replace(exe), esc.Replace(filepath.Join(h, "Library", "Logs", "tmreportwell.log")))
	if err := os.MkdirAll(filepath.Dir(agentPath()), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(agentPath(), []byte(pl), 0o644); err != nil {
		return err
	}
	// bootout fails when the agent is not loaded, which is fine here.
	_ = exec.Command("/bin/launchctl", "bootout", launchDomain()+"/"+agentLabel).Run()
	if _, err := run(30*time.Second, "/bin/launchctl", "bootstrap", launchDomain(), agentPath()); err != nil {
		return err
	}
	fmt.Println("installed", agentPath())
	return nil
}

func cmdUninstall() error {
	// bootout fails when the agent is not loaded, which is fine here.
	_ = exec.Command("/bin/launchctl", "bootout", launchDomain()+"/"+agentLabel).Run()
	return os.Remove(agentPath())
}

func main() {
	cmd := ""
	if len(os.Args) == 2 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "--version", "-V":
		fmt.Println("tmreportwell", version)
	case "report":
		err = cmdReport()
	case "run":
		err = cmdRun(false)
	case "send":
		err = cmdRun(true)
	case "install":
		err = cmdInstall()
	case "uninstall":
		err = cmdUninstall()
	default:
		fmt.Fprint(os.Stderr, `usage: tmreportwell <command>

  report     print the report, change nothing
  run        collect log messages, mail the report if it is due
  send       collect log messages, mail the report now
  install    run hourly as a launch agent
  uninstall  remove the launch agent
  --version  print the version
`)
		os.Exit(2)
	}
	if err != nil {
		fatal(err)
	}
}
