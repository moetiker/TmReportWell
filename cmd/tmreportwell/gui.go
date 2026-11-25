package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"net/mail"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// gui is the configuration form. It is served on the loopback address for one
// session; the token keeps other local users and web pages out.
type gui struct {
	host, token string
	load        func() (*Config, error)
	save        func(*Config) error
	hasPassword func(account string) bool
	setPassword func(account, password string) error
	testMail    func(*Config) error
	used        chan struct{} // a request came in
	done        chan struct{} // the user closed the form
}

type guiPage struct {
	C            *Config
	Token        string
	OK, Err      string
	PasswordKept bool
	Closed       bool
}

func (g *gui) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	// A web page whose name was rebound to 127.0.0.1 sends its own name.
	if r.Host != g.host {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Form.Get("t")), []byte(g.token)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	select {
	case g.used <- struct{}{}:
	default:
	}
	p := guiPage{Token: g.token}
	switch r.Method {
	case http.MethodGet:
		c, err := g.load()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			p.Err = err.Error()
		}
		p.C = c
	case http.MethodPost:
		if r.PostForm.Get("action") == "close" {
			p.Closed = true
			break
		}
		var err error
		p.C, err = g.submit(r)
		switch {
		case err != nil:
			p.Err = err.Error()
		case r.PostForm.Get("action") == "test":
			if err := g.testMail(p.C); err != nil {
				p.Err = "Saved, but the test mail failed: " + err.Error()
			} else {
				p.OK = "Saved. The mail server accepted a test mail to " + p.C.To + "."
			}
		default:
			p.OK = "Saved."
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if p.C != nil {
		p.PasswordKept = p.C.SMTPUser != "" && g.hasPassword(p.C.SMTPUser)
	}
	h.Set("Content-Type", "text/html; charset=utf-8")
	_ = guiForm.Execute(w, p)
	if p.Closed {
		close(g.done)
	}
}

// submit stores the form. The password goes first: a configuration whose
// password is missing would let the next hourly run fail.
func (g *gui) submit(r *http.Request) (*Config, error) {
	f := func(k string) string { return strings.TrimSpace(r.PostForm.Get(k)) }
	c := &Config{SMTPHost: f("smtp_host"), SMTPUser: f("smtp_user"), From: f("from"), To: f("to"), SendAt: f("send_at")}
	var err error
	if c.SMTPPort, err = strconv.Atoi(f("smtp_port")); err != nil {
		return c, fmt.Errorf("the port must be a number")
	}
	if c.WarnDays, err = strconv.ParseFloat(f("warn_days"), 64); err != nil {
		return c, fmt.Errorf("the days for WARNING must be a number")
	}
	if c.BadDays, err = strconv.ParseFloat(f("problem_days"), 64); err != nil {
		return c, fmt.Errorf("the days for PROBLEM must be a number")
	}
	if err := c.validate(); err != nil {
		return c, err
	}
	if pw := r.PostForm.Get("password"); pw != "" {
		if err := g.setPassword(c.SMTPUser, pw); err != nil {
			return c, fmt.Errorf("storing the password: %w", err)
		}
	} else if !g.hasPassword(c.SMTPUser) {
		return c, fmt.Errorf("no password is stored for %s, enter it", c.SMTPUser)
	}
	return c, g.save(c)
}

// validate names the first field that cannot work.
func (c *Config) validate() error {
	for _, f := range [][2]string{{"smtp_host", c.SMTPHost}, {"smtp_user", c.SMTPUser}, {"from", c.From}, {"to", c.To}} {
		if f[1] == "" {
			return fmt.Errorf("%s is missing", f[0])
		}
	}
	if c.SMTPPort < 1 || c.SMTPPort > 65535 {
		return fmt.Errorf("smtp_port must be between 1 and 65535, not %d", c.SMTPPort)
	}
	for _, f := range [][2]string{{"from", c.From}, {"to", c.To}} {
		if _, err := mail.ParseAddress(f[1]); err != nil {
			return fmt.Errorf("%s is not a mail address: %q", f[0], f[1])
		}
	}
	if _, err := time.Parse("15:04", c.SendAt); err != nil {
		return fmt.Errorf("send_at must look like 09:00, not %q", c.SendAt)
	}
	if !(c.WarnDays > 0 && c.BadDays >= c.WarnDays) {
		return fmt.Errorf("warn_days must be above 0 and problem_days at least as large")
	}
	return nil
}

func hasKeychainPassword(account string) bool {
	_, err := run(30*time.Second, "/usr/bin/security", "find-generic-password", "-s", keychainService, "-a", account)
	return err == nil
}

// keychainCommand is one line for "security -i". The password travels on
// standard input and in hex: an argument would show up in the process list,
// and hex needs no quoting.
func keychainCommand(account, password string) (string, error) {
	if strings.ContainsFunc(account, func(r rune) bool { return r <= ' ' || r == 0x7f || strings.ContainsRune(`"'\`, r) }) {
		return "", fmt.Errorf("the login name contains a space, a quote or a backslash")
	}
	// "security -w" prints a password with other characters as hex, and the
	// login would then send that hex.
	if strings.ContainsFunc(password, func(r rune) bool { return r < ' ' || r > '~' }) {
		return "", fmt.Errorf("the password contains characters outside ASCII")
	}
	return fmt.Sprintf("add-generic-password -U -s %s -a \"%s\" -X %s\n", keychainService, account, hex.EncodeToString([]byte(password))), nil
}

func setKeychainPassword(account, password string) error {
	line, err := keychainCommand(account, password)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := exec.CommandContext(ctx, "/usr/bin/security", "-i")
	c.Stdin = strings.NewReader(line)
	if err := c.Run(); err != nil {
		return fmt.Errorf("security: %w", err)
	}
	// "security -i" can exit 0 after a command that failed.
	if got, err := keychainPassword(account); err != nil || got != password {
		return fmt.Errorf("the keychain did not take the password")
	}
	return nil
}

func sendTestMail(c *Config) error {
	const text = "This is a test mail. The mail settings of TmReportWell work."
	return sendMail(c, Report{Subject: reportTitle + ": test mail", Body: text + "\n\n-- \n" + origin() + "\n" + homeURL + "\n"})
}

func cmdConfig() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	tok := make([]byte, 24)
	if _, err := rand.Read(tok); err != nil {
		return err
	}
	g := &gui{host: ln.Addr().String(), token: hex.EncodeToString(tok), load: loadConfig, save: saveConfig,
		hasPassword: hasKeychainPassword, setPassword: setKeychainPassword, testMail: sendTestMail,
		used: make(chan struct{}, 1), done: make(chan struct{})}
	srv := &http.Server{Handler: http.MaxBytesHandler(g, 64<<10), ReadHeaderTimeout: 10 * time.Second}
	failed := make(chan error, 1)
	go func() { failed <- srv.Serve(ln) }()
	url := "http://" + g.host + "/?t=" + g.token
	if err := exec.Command("/usr/bin/open", url).Run(); err != nil {
		fmt.Println("open this address in a browser:", url)
	}
	fmt.Println("The form is open in the browser. Close it there, or press Ctrl-C here.")
	const idle = 15 * time.Minute
	for timer := time.NewTimer(idle); ; {
		select {
		case <-g.used:
			timer.Reset(idle)
			continue
		case err := <-failed:
			return err
		case <-g.done:
		case <-timer.C:
			fmt.Println("closed after 15 minutes without use")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(ctx)
	}
}

var guiForm = template.Must(template.New("gui").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Timemachine Report settings</title>
<style>
:root{color-scheme:light dark;--bg:#eef2f6;--card:#fff;--ink:#1e293b;--soft:#526072;--line:#b6c2cf;--key:#1d4f8c;--on:#fff}
@media(prefers-color-scheme:dark){:root{--bg:#10161d;--card:#1a232d;--ink:#e6ecf2;--soft:#a3b1c0;--line:#4a5a6b;--key:#8bbcf5;--on:#10161d}}
*{box-sizing:border-box}body{margin:0;padding:24px 16px;background:var(--bg);color:var(--ink);font:15px/1.5 -apple-system,sans-serif}
main{max-width:560px;margin:auto;background:var(--card);border-radius:10px;padding:24px 26px}
h1{font-size:22px;margin:0 0 4px}fieldset{border:0;padding:0;margin:22px 0 0}legend{font-weight:700;font-size:17px;padding:0}
label{display:block;margin-top:12px;font-weight:600}small{display:block;font-weight:400;color:var(--soft)}
input{width:100%;margin-top:4px;padding:8px 10px;font:inherit;font-weight:400;color:inherit;background:transparent;border:1px solid var(--line);border-radius:6px}
input:focus-visible,button:focus-visible{outline:2px solid var(--key);outline-offset:2px}
.two{display:grid;grid-template-columns:2fr 1fr;gap:0 14px}.even{grid-template-columns:1fr 1fr}
.msg{margin:16px 0 0;padding:10px 14px;border-radius:6px;color:#fff;overflow-wrap:anywhere}.ok{background:#15803d}.err{background:#b91c1c}
.row{display:flex;flex-wrap:wrap;gap:10px;margin-top:26px}button{font:inherit;font-weight:600;padding:9px 16px;border-radius:6px;border:1px solid var(--key);background:transparent;color:var(--key);cursor:pointer}
button.main{background:var(--key);color:var(--on)}
</style></head><body><main>
<h1>Timemachine Report settings</h1>
{{if .Closed}}<p>The form is closed. This window can go.</p>{{else}}
{{if .OK}}<p class="msg ok" role="status">{{.OK}}</p>{{end}}{{if .Err}}<p class="msg err" role="alert">{{.Err}}</p>{{end}}
<form method="post" action="/" autocomplete="off"><input type="hidden" name="t" value="{{.Token}}">
{{with .C}}<fieldset><legend>Mail</legend>
<label>Recipient<input name="to" value="{{.To}}" required></label>
<label>Sender<input name="from" value="{{.From}}" required></label>
</fieldset><fieldset><legend>Mail server</legend>
<div class="two"><label>Server<input name="smtp_host" value="{{.SMTPHost}}" required></label>
<label>Port<input name="smtp_port" type="number" min="1" max="65535" value="{{.SMTPPort}}" required></label></div>
<label>Login name<input name="smtp_user" value="{{.SMTPUser}}" required></label>{{end}}
<label>Password<input name="password" type="password" autocomplete="new-password"><small>{{if .PasswordKept}}A password is stored in the keychain. Leave this empty to keep it.{{else}}It is stored in the login keychain.{{end}}</small></label>
{{with .C}}</fieldset><fieldset><legend>Report</legend>
<label>Send once per day from<input name="send_at" type="time" value="{{.SendAt}}" required></label>
<div class="two even"><label>WARNING after days<input name="warn_days" type="number" min="0.1" step="any" value="{{.WarnDays}}" required></label>
<label>PROBLEM after days<input name="problem_days" type="number" min="0.1" step="any" value="{{.BadDays}}" required></label></div>
</fieldset>{{end}}
<div class="row"><button class="main" name="action" value="save">Save</button><button name="action" value="test">Save and send test mail</button><button name="action" value="close" formnovalidate>Close</button></div>
</form>{{end}}</main></body></html>
`))
