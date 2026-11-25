package main

import (
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

type guiFake struct {
	saved    *Config
	password string
	stored   bool
	tests    int
	testErr  error
}

func (f *guiFake) gui() *gui {
	return &gui{host: "127.0.0.1:1", token: "tok",
		load:        func() (*Config, error) { return defaultConfig(), os.ErrNotExist },
		save:        func(c *Config) error { f.saved = c; return nil },
		hasPassword: func(string) bool { return f.stored },
		setPassword: func(_, p string) error { f.password, f.stored = p, true; return nil },
		testMail:    func(*Config) error { f.tests++; return f.testErr },
		used:        make(chan struct{}, 1), done: make(chan struct{})}
}

func request(g *gui, method, host string, form url.Values) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://"+host+"/?"+map[bool]string{true: form.Encode()}[method == http.MethodGet], nil)
	if method == http.MethodPost {
		r = httptest.NewRequest(method, "http://"+host+"/", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	w := httptest.NewRecorder()
	g.ServeHTTP(w, r)
	return w
}

func goodForm() url.Values {
	return url.Values{"t": {"tok"}, "action": {"save"}, "smtp_host": {"mail.example.com"}, "smtp_port": {"587"},
		"smtp_user": {"me"}, "password": {"pw"}, "from": {"a@example.com"}, "to": {"b@example.com"},
		"send_at": {"08:30"}, "warn_days": {"2"}, "problem_days": {"7"}}
}

func TestGUIRefusesStrangers(t *testing.T) {
	f := &guiFake{}
	form := goodForm()
	if w := request(f.gui(), "POST", "evil.example.com", form); w.Code != http.StatusForbidden {
		t.Errorf("foreign host: %d", w.Code)
	}
	form.Set("t", "wrong")
	if w := request(f.gui(), "POST", "127.0.0.1:1", form); w.Code != http.StatusForbidden {
		t.Errorf("wrong token: %d", w.Code)
	}
	if w := request(f.gui(), "GET", "127.0.0.1:1", url.Values{}); w.Code != http.StatusForbidden {
		t.Errorf("no token: %d", w.Code)
	}
	if f.saved != nil || f.password != "" {
		t.Error("a refused request changed something")
	}
}

func TestGUISave(t *testing.T) {
	f := &guiFake{}
	g := f.gui()
	if w := request(g, "GET", "127.0.0.1:1", url.Values{"t": {"tok"}}); w.Code != 200 || !strings.Contains(w.Body.String(), `value="587"`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	w := request(g, "POST", "127.0.0.1:1", goodForm())
	if f.saved == nil || f.saved.SendAt != "08:30" || f.password != "pw" || f.tests != 0 {
		t.Fatalf("%+v %s", f, w.Body)
	}
	if strings.Contains(w.Body.String(), `"pw"`) {
		t.Error("the page repeats the password")
	}
	form := goodForm()
	form.Set("action", "test")
	form.Del("password")
	f.testErr = errors.New("<refused>")
	w = request(g, "POST", "127.0.0.1:1", form)
	if f.tests != 1 || f.password != "pw" || !strings.Contains(w.Body.String(), "&lt;refused&gt;") {
		t.Errorf("%+v %s", f, w.Body)
	}
	request(g, "POST", "127.0.0.1:1", url.Values{"t": {"tok"}, "action": {"close"}})
	select {
	case <-g.done:
	default:
		t.Error("close does not end the form")
	}
}

func TestGUIBadInput(t *testing.T) {
	for field, value := range map[string]string{"smtp_host": "", "smtp_port": "70000", "to": "nobody", "send_at": "25:00",
		"warn_days": "9", "problem_days": "NaN", "password": ""} {
		f := &guiFake{}
		form := goodForm()
		form.Set(field, value)
		if w := request(f.gui(), "POST", "127.0.0.1:1", form); f.saved != nil || !strings.Contains(w.Body.String(), `role="alert"`) {
			t.Errorf("%s=%q was accepted", field, value)
		}
	}
}

func TestKeychainCommand(t *testing.T) {
	line, err := keychainCommand("me@example.com", `p"w 'x\`)
	if err != nil || strings.Contains(line, `p"w`) || !strings.Contains(line, hex.EncodeToString([]byte(`p"w 'x\`))) || strings.Count(line, "\n") != 1 {
		t.Errorf("%q %v", line, err)
	}
	for _, c := range [][2]string{{"a\nb", "x"}, {`a"b`, "x"}, {"a b", "x"}, {"me", "grün"}, {"me", "a\nb"}} {
		if _, err := keychainCommand(c[0], c[1]); err == nil {
			t.Errorf("%q was accepted", c)
		}
	}
}
