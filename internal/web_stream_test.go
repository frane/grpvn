package internal

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebIsExplicitServerOption(t *testing.T) {
	db := newTestDB(t)
	for _, enabled := range []bool{false, true} {
		h, err := CoordinatorHandler(db, CoordinatorOptions{Web: enabled})
		if err != nil {
			t.Fatal(err)
		}
		root := httptest.NewRecorder()
		h.ServeHTTP(root, httptest.NewRequest("GET", "/", nil))
		if enabled {
			if root.Code != 200 || !strings.Contains(root.Body.String(), "<div id=\"root\">") {
				t.Fatal("web app missing", root.Code)
			}
		} else if root.Code != http.StatusTemporaryRedirect || root.Header().Get("Location") != "/llm.txt" {
			t.Fatal("headless coordinator unexpectedly serves UI")
		}
		private := httptest.NewRecorder()
		h.ServeHTTP(private, httptest.NewRequest("GET", "/v1/chat/events", nil))
		if enabled && private.Code != 401 {
			t.Fatal("anonymous SSE stream allowed")
		}
	}
}

func TestWebLiveFeedOutlivesHTTPWriteTimeout(t *testing.T) {
	db := newTestDB(t)
	if _, err := LocalHumanTicket(db, "owner"); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := db.QueryRow("SELECT value FROM server_meta WHERE key='local_human_id'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	access, _ := newChannelAccess(CoordinatorOptions{})
	access.db = db
	s := &webServer{db: db, access: access, local: true}
	login := httptest.NewRecorder()
	if err := s.session(login, id); err != nil {
		t.Fatal(err)
	}
	p, err := findPrincipal(db, "id=?", id)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.events(w, r, p) }))
	server.Config.WriteTimeout = 20 * time.Millisecond
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	for _, cookie := range login.Result().Cookies() {
		req.AddCookie(cookie)
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	lines := bufio.NewScanner(response.Body)
	for lines.Scan() {
		if lines.Text() == "" {
			break
		}
	}
	time.Sleep(75 * time.Millisecond)
	if _, err := Send(db, "peer", "#general", "after the ordinary write timeout", "", false); err != nil {
		t.Fatal(err)
	}
	for lines.Scan() {
		if lines.Text() == "event: changed" {
			return
		}
	}
	t.Fatalf("live feed closed at ordinary HTTP write timeout: %v", lines.Err())
}
