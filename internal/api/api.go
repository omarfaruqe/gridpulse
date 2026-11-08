// Package api serves the warehouse over a read-only JSON API.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// API holds what the handlers need.
type API struct {
	Pool *pgxpool.Pool
	Log  *slog.Logger
	Now  func() time.Time
}

// New returns an API that reads the clock from time.Now.
func New(pool *pgxpool.Pool, log *slog.Logger) *API {
	return &API{Pool: pool, Log: log, Now: time.Now}
}

// Handler returns the router. Everything is under /api/v1.
func (a *API) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer, middleware.Timeout(20*time.Second), a.logRequests, securityHeaders)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) { writeError(w, http.StatusNotFound, "not found") })
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed; this API is read-only")
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", a.health)
		r.Group(func(r chi.Router) {
			r.Use(cacheFor(60 * time.Second))
			r.Get("/provinces", a.provinces)
			r.Get("/fuels", a.fuels)
			r.Get("/load", a.load)
			r.Get("/generation", a.generation)
			r.Get("/prices", a.prices)
			r.Get("/weather", a.weather)
			r.Get("/load-weather", a.loadWeather)
			r.Get("/reconciliation", a.reconciliation)
		})
		r.With(cacheFor(30*time.Second)).Get("/pipeline", a.pipeline)
	})
	return r
}

type envelope struct {
	Data any            `json:"data"`
	Meta map[string]any `json:"meta,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		http.Error(w, `{"error":"internal error"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail answers a request whose query failed. The client gets no database detail.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	var bad badRequest
	if errors.As(err, &bad) {
		writeError(w, http.StatusBadRequest, bad.Error())
		return
	}
	a.Log.Error("query failed", "path", r.URL.Path, "error", err, "request_id", middleware.GetReqID(r.Context()))
	writeError(w, http.StatusInternalServerError, "internal error")
}

// list runs a query and answers with its rows, each scanned into a T by column name.
func list[T any](a *API, w http.ResponseWriter, r *http.Request, meta map[string]any, sql string, args ...any) {
	rows, err := a.Pool.Query(r.Context(), sql, args...)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	data, err := pgx.CollectRows(rows, pgx.RowToStructByName[T])
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if data == nil {
		data = []T{} // an empty result is [] and not null
	}
	meta["rows"] = len(data)
	writeJSON(w, http.StatusOK, envelope{Data: data, Meta: meta})
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.Pool.Ping(ctx); err != nil {
		a.Log.Error("health check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *API) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		status := ww.Status()
		if status == 0 {
			status = http.StatusOK
		}
		a.Log.Info("request", "method", r.Method, "path", r.URL.Path, "status", status,
			"bytes", ww.BytesWritten(), "took", time.Since(start).Round(time.Millisecond),
			"request_id", middleware.GetReqID(r.Context()))
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func cacheFor(d time.Duration) func(http.Handler) http.Handler {
	value := "public, max-age=" + strconv.Itoa(int(d.Seconds()))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", value)
			next.ServeHTTP(w, r)
		})
	}
}
