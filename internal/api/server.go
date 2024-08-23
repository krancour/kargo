package api

import (
	"net"
	"net/http"
	"time"
)

type server struct {
	ready bool
}

type Server interface {
	Serve(l net.Listener) error
}

func NewServer() Server {
	return &server{
		// A 50% chance of being ready
		ready: time.Now().UnixNano()%2 == 0,
	}
}

func (s *server) Serve(l net.Listener) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if s.ready {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("not ready"))
		}
	})
	return (&http.Server{
		Handler:           mux,
		ReadHeaderTimeout: time.Minute,
	}).Serve(l)
}
