// tmwell mails a daily report about what Time Machine did and did not do.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const agentLabel = "tmwell"

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

// State survives between the hourly runs.
type State struct {
	Cursor   time.Time         `json:"cursor"`    // newest log event already collected
	LastSent time.Time         `json:"last_sent"` // end of the last mailed period
	Groups   map[string]*Group `json:"groups"`
}

func dir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		fatal(err)
	}
	return filepath.Join(h, "Library", "Application Support", "TmWell")
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "%s tmwell: %v\n", time.Now().Format(time.DateTime), err)
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
	return s, err
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

// due reports whether today's mail is still to be sent.
func due(sendAt string, lastSent, now time.Time) bool {
	t, err := time.Parse("15:04", sendAt)
	if err != nil {
		return false
	}
	slot := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, now.Location())
	return !now.Before(slot) && lastSent.Before(slot)
}

func report(c *Config, s *State, now time.Time, logErr error) Report {
	st, err := readTMState()
	return buildReport(reportInput{Host: hostname(), From: s.LastSent, To: now, State: st, StateErr: err,
		LogErr: logErr, Groups: s.Groups, WarnDays: c.WarnDays, BadDays: c.BadDays})
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
	_, logErr := collectLog(s.Cursor, s.Groups)
	r := report(c, s, now, logErr)
	fmt.Printf("Subject: %s\n\n%s", r.Subject, r.Body)
	return nil
}

func cmdRun(force bool) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	now := time.Now()
	s, err := loadState(now)
	if err != nil {
		return err
	}
	var logErr error
	s.Cursor, logErr = collectLog(s.Cursor, s.Groups)
	// Saved before mailing: the log drops these messages after about a day, so
	// they must outlive a mail server that stays unreachable for longer.
	if err := saveState(s); err != nil {
		return err
	}
	if !force && !due(c.SendAt, s.LastSent, now) {
		return logErr
	}
	if err := sendMail(c, report(c, s, now, logErr)); err != nil {
		return fmt.Errorf("sending mail: %w", err)
	}
	s.LastSent, s.Groups = now, map[string]*Group{}
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
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
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
`, agentLabel, esc.Replace(exe), esc.Replace(filepath.Join(h, "Library", "Logs", "tmwell.log")))
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
		fmt.Println("tmwell", version)
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
		fmt.Fprint(os.Stderr, `usage: tmwell <command>

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
