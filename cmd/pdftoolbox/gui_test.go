package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGUISessionLifecycle(t *testing.T) {
	stop := make(chan struct{}, 1)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "界面可用") })
	ts := httptest.NewUnstartedServer(nil)
	address := "http://" + ts.Listener.Addr().String()
	ts.Config.Handler = guiSessionHandler(next, address, "test-session", stop)
	ts.Start()
	defer ts.Close()
	client := ts.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r, err := client.Get(address + "/")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatalf("unauthenticated status=%d", r.StatusCode)
	}
	r, err = client.Get(address + "/?session=test-session")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 303 || len(r.Cookies()) != 1 {
		t.Fatal("missing bootstrap redirect/cookie")
	}
	cookie := r.Cookies()[0]
	if !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatal("unsafe session cookie")
	}
	req, _ := http.NewRequest("GET", address+"/", nil)
	req.AddCookie(cookie)
	r, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if r.StatusCode != 200 || !strings.Contains(string(body), "界面可用") {
		t.Fatal("interface unavailable")
	}
	req, _ = http.NewRequest("POST", address+"/api/shutdown", nil)
	req.AddCookie(cookie)
	req.Header.Set("Origin", "https://example.com")
	r, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatal("cross origin accepted")
	}
	select {
	case <-stop:
		t.Fatal("cross origin stopped service")
	default:
	}
	req, _ = http.NewRequest("POST", address+"/api/shutdown", nil)
	req.AddCookie(cookie)
	req.Header.Set("Origin", address)
	r, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal("shutdown failed")
	}
	select {
	case <-stop:
	default:
		t.Fatal("shutdown signal missing")
	}
}

func TestGUIRejectsForeignHost(t *testing.T) {
	h := guiSessionHandler(http.NotFoundHandler(), "http://127.0.0.1:12345", "secret", make(chan struct{}, 1))
	r := httptest.NewRequest("GET", "http://evil.test/?session=secret", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestGUIUsesAvailableLoopbackPort(t *testing.T) {
	a, err := listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	u, _ := url.Parse("http://" + a.Addr().String())
	if u.Hostname() != "127.0.0.1" || a.Addr().String() == b.Addr().String() {
		t.Fatal("invalid listener")
	}
}
