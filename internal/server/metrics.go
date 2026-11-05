package server

import (
	"net/http"
	"time"
)

func (s *Server) metricsMiddleware(next http.Handler) http.Handler {
	if s.Metrics == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.Metrics.HTTPInFlight(1)
		defer s.Metrics.HTTPInFlight(-1)

		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)

		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		s.Metrics.ObserveHTTP(r.Method, route, status, time.Since(start))
	})
}
