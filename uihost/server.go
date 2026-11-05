// Package uihost starts the shared human interface on loopback. The Wails executable use this exact server and chat API.
package uihost

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/frane/grpvn/internal"
)

type Server struct {
	URL    string
	db     *sql.DB
	http   *http.Server
	cancel context.CancelFunc
	done   chan struct{}
}

func Start(parent context.Context, name, listen string) (*Server, error) {
	db, err := internal.OpenDB()
	if err != nil {
		return nil, err
	}
	if listen == "" {
		listen = "127.0.0.1:0"
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		db.Close()
		return nil, fmt.Errorf("the local human interface must listen on a loopback IP")
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		db.Close()
		return nil, err
	}
	handler, err := internal.CoordinatorHandler(db, internal.CoordinatorOptions{LocalUI: true})
	if err != nil {
		ln.Close()
		db.Close()
		return nil, err
	}
	ticket, err := internal.LocalHumanTicket(db, name)
	if err != nil {
		ln.Close()
		db.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	s := &Server{URL: "http://" + ln.Addr().String() + "/#ticket=" + ticket, db: db, http: &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}, cancel: cancel, done: make(chan struct{})}
	go func() { _ = s.http.Serve(ln) }()
	go func() { defer close(s.done); _ = internal.RunDaemon(ctx, db, time.Second, io.Discard) }()
	go func() {
		<-ctx.Done()
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = s.http.Shutdown(shutdown)
	}()
	return s, nil
}
func (s *Server) Close() error { s.cancel(); _ = s.http.Close(); <-s.done; return s.db.Close() }
