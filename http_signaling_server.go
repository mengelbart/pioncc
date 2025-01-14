package pioncc

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/pion/webrtc/v4"
)

type SessionDescriptionHandler interface {
	HandleSessionDescription(webrtc.SessionDescription) (*webrtc.SessionDescription, error)
}

type CandidateHandler interface {
	HandleCandidate(webrtc.ICECandidateInit) error
}

type HTTPSignalingServer struct {
	httpServer *http.Server
	sdh        SessionDescriptionHandler
	ch         CandidateHandler
}

func NewHTTPSignalingServer(addr string, sdh SessionDescriptionHandler, ch CandidateHandler) *HTTPSignalingServer {
	sm := http.NewServeMux()
	hss := &HTTPSignalingServer{
		httpServer: &http.Server{Addr: addr, Handler: requestLogger(sm)},
		sdh:        sdh,
		ch:         ch,
	}
	sm.HandleFunc("POST /offer", hss.sessionDescriptionHandler())
	sm.HandleFunc("POST /candidate", hss.CandidateHandler())
	return hss
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("request", "method", r.Method, "url", r.URL)
		next.ServeHTTP(w, r)
	})
}

func (s *HTTPSignalingServer) ListenAndServe() error {
	slog.Info("server listening", "addr", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

func (s *HTTPSignalingServer) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *HTTPSignalingServer) sessionDescriptionHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.sdh == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		sd, err := decodeSessionDescription(string(body))
		if err != nil {
			slog.Error("got malformed session description", "err", err, "description", string(body))
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "bad request: %v", err.Error())
			return
		}
		answer, err := s.sdh.HandleSessionDescription(sd)
		if err != nil {
			slog.Error("failed to handle session description", "err", err, "description", sd)
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "bad request: %v", err.Error())
			return
		}
		response, err := encodeSessionDescription(*answer)
		if err != nil {
			slog.Error("failed to encode session description", "err", err, "description", answer)
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, "internal error: %v", err.Error())
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, "%s", response)
	}
}

func (s *HTTPSignalingServer) CandidateHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		c, err := decodeCandidate(body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "bad request: %v", err.Error())
			return
		}
		if s.ch != nil {
			if err := s.ch.HandleCandidate(c); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, "bad request: %v", err.Error())
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}
}
