// Package web serves the dashboard and the pipeline-health page. Pages are Go
// templates; charts are drawn in the browser from the JSON API, and htmx keeps
// the health page current.
package web

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	stdstrings "strings"
	"time"

	"github.com/omarfaruqe/gridpulse/internal/api"
)

//go:embed templates static
var assets embed.FS

// Web renders the pages. Anything it does not serve goes to the next handler (the API).
type Web struct {
	api   *api.API
	log   *slog.Logger
	pages map[string]*template.Template
}

func New(a *api.API, log *slog.Logger) (*Web, error) {
	funcs := template.FuncMap{"stamp": stamp, "stampTime": stampTime, "dur": dur}
	w := &Web{api: a, log: log, pages: map[string]*template.Template{}}
	for _, p := range []string{"dashboard", "pipeline"} {
		t, err := template.New(p).Funcs(funcs).Option("missingkey=error").
			ParseFS(assets, "templates/layout.html", "templates/"+p+".html", "templates/pipeline_body.html")
		if err != nil {
			return nil, fmt.Errorf("parse %s template: %w", p, err)
		}
		w.pages[p] = t
	}
	return w, nil
}

// Handler serves the pages and static files and passes every other request to next.
func (w *Web) Handler(next http.Handler) http.Handler {
	static, _ := fs.Sub(assets, "static")
	files := http.StripPrefix("/static/", http.FileServerFS(static))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", w.dashboard)
	mux.HandleFunc("GET /pipeline", w.pipelinePage)
	mux.HandleFunc("GET /ui/pipeline", w.pipelineFragment)
	mux.HandleFunc("GET /static/", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "public, max-age=3600")
		files.ServeHTTP(rw, r)
	})
	mux.Handle("/", next)
	return securityHeaders(mux)
}

// The pages load scripts and styles only from this origin and are never framed.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		h := rw.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(rw, r)
	})
}

type base struct {
	Lang      string
	SwitchURL string
	SwitchTo  string
	T         map[string]string
	Active    string
}

func (w *Web) base(rw http.ResponseWriter, r *http.Request, active string) base {
	lang := "en"
	switch q := r.URL.Query().Get("lang"); {
	case q == "en" || q == "fr":
		lang = q
		http.SetCookie(rw, &http.Cookie{Name: "lang", Value: q, Path: "/", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode})
	default:
		if c, err := r.Cookie("lang"); err == nil && (c.Value == "en" || c.Value == "fr") {
			lang = c.Value
		} else if stdstrings.HasPrefix(stdstrings.ToLower(r.Header.Get("Accept-Language")), "fr") {
			lang = "fr"
		}
	}
	other := map[string]string{"en": "fr", "fr": "en"}[lang]
	q := r.URL.Query()
	q.Set("lang", other)
	return base{Lang: lang, SwitchTo: other, SwitchURL: r.URL.Path + "?" + q.Encode(), T: text[lang], Active: active}
}

func (w *Web) render(rw http.ResponseWriter, page, name string, data any) {
	var buf bytes.Buffer
	if err := w.pages[page].ExecuteTemplate(&buf, name, data); err != nil {
		w.log.Error("render failed", "page", page, "error", err)
		http.Error(rw, "internal error", http.StatusInternalServerError)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-cache")
	_, _ = buf.WriteTo(rw)
}

type choice struct {
	Value, Label, URL string
	Selected          bool
}

type dashboardView struct {
	base
	Province     string
	Provinces    []choice
	Ranges       []choice
	LoadURL      string
	GenURL       string
	PriceURL     string
	ScatterURL   string
	HasGen       bool
	HasPrice     bool
	ProvinceName string
}

var rangeDays = []int{1, 7, 30}

func (w *Web) dashboard(rw http.ResponseWriter, r *http.Request) {
	b := w.base(rw, r, "dashboard")
	q := r.URL.Query()
	prov := q.Get("p")
	if _, ok := provinceNames["en"][prov]; !ok {
		prov = "ON" // unknown values fall back, and are never echoed
	}
	days := 7
	for _, d := range rangeDays {
		if q.Get("days") == fmt.Sprint(d) {
			days = d
		}
	}
	today := time.Now().UTC()
	from, to := today.AddDate(0, 0, -(days-1)).Format("2006-01-02"), today.Format("2006-01-02")
	span := "province=" + prov + "&from=" + from + "&to=" + to

	v := dashboardView{
		base: b, Province: prov, ProvinceName: provinceNames[b.Lang][prov],
		LoadURL: "/api/v1/load?" + span, GenURL: "/api/v1/generation?" + span,
		PriceURL: "/api/v1/prices?" + span, ScatterURL: "/api/v1/load-weather?" + span,
		HasGen: prov == "ON", HasPrice: prov != "QC",
	}
	for _, code := range []string{"QC", "ON", "AB"} {
		v.Provinces = append(v.Provinces, choice{Value: code, Label: provinceNames[b.Lang][code], Selected: code == prov,
			URL: "/?" + url.Values{"p": {code}, "days": {fmt.Sprint(days)}}.Encode()})
	}
	for _, d := range rangeDays {
		v.Ranges = append(v.Ranges, choice{Value: fmt.Sprint(d), Label: fmt.Sprintf(b.T["last_n_days"], d), Selected: d == days,
			URL: "/?" + url.Values{"p": {prov}, "days": {fmt.Sprint(d)}}.Encode()})
	}
	w.render(rw, "dashboard", "layout", v)
}

type pipelineView struct {
	base
	H api.PipelineHealth
}

func (w *Web) pipelineView(rw http.ResponseWriter, r *http.Request) (pipelineView, bool) {
	b := w.base(rw, r, "pipeline")
	h, err := w.api.Health(r)
	if err != nil {
		w.log.Error("pipeline health failed", "error", err)
		http.Error(rw, "internal error", http.StatusInternalServerError)
		return pipelineView{}, false
	}
	return pipelineView{base: b, H: h}, true
}

func (w *Web) pipelinePage(rw http.ResponseWriter, r *http.Request) {
	if v, ok := w.pipelineView(rw, r); ok {
		w.render(rw, "pipeline", "layout", v)
	}
}

// pipelineFragment is what htmx swaps in every 30 seconds.
func (w *Web) pipelineFragment(rw http.ResponseWriter, r *http.Request) {
	if v, ok := w.pipelineView(rw, r); ok {
		w.render(rw, "pipeline", "pipeline_body", v)
	}
}

// stamp shows an RFC 3339 string as "2006-01-02 15:04 UTC".
func stamp(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return stampTime(t)
}

func stampTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04") + " UTC" }

// dur shows seconds as "3 h 05 min"; negative values keep a minus sign.
func dur(s int64) string {
	sign := ""
	if s < 0 {
		sign, s = "−", -s
	}
	d := time.Duration(s) * time.Second
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%s%d d %d h", sign, int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%s%d h %02d min", sign, int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%s%d min", sign, int(d.Minutes()))
	}
}
