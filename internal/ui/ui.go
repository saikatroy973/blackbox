// Package ui serves the server-rendered HTMX dashboard.
package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"time"

	"github.com/saikat/blackbox/internal/eval"
	"github.com/saikat/blackbox/internal/store"
	"github.com/saikat/blackbox/web"
)

// Handler serves dashboard pages and fragments.
type Handler struct {
	store       store.Store
	listTmpl    *template.Template
	traceTmpl   *template.Template
	partialTmpl *template.Template
	evalsTmpl   *template.Template
	evalRunTmpl *template.Template
	diffTmpl    *template.Template
}

// New parses the embedded templates and returns a Handler.
func New(st store.Store) (*Handler, error) {
	funcs := template.FuncMap{
		"shortID":        shortID,
		"formatDuration": formatDuration,
		"formatCost":     formatCost,
		"formatTime": func(t time.Time) string {
			return t.Local().Format("2006-01-02 15:04:05.000")
		},
		"jsonPretty": jsonPretty,
	}

	list, err := template.New("").Funcs(funcs).ParseFS(web.FS,
		"templates/base.html", "templates/error.html", "templates/traces.html")
	if err != nil {
		return nil, fmt.Errorf("parse list templates: %w", err)
	}
	detail, err := template.New("").Funcs(funcs).ParseFS(web.FS,
		"templates/base.html", "templates/error.html", "templates/trace.html", "templates/span.html")
	if err != nil {
		return nil, fmt.Errorf("parse trace templates: %w", err)
	}
	partials, err := template.New("").Funcs(funcs).ParseFS(web.FS,
		"templates/error.html", "templates/span.html")
	if err != nil {
		return nil, fmt.Errorf("parse partial templates: %w", err)
	}
	evals, err := template.New("").Funcs(funcs).ParseFS(web.FS,
		"templates/base.html", "templates/error.html", "templates/evals.html")
	if err != nil {
		return nil, fmt.Errorf("parse eval templates: %w", err)
	}
	evalRun, err := template.New("").Funcs(funcs).ParseFS(web.FS,
		"templates/base.html", "templates/error.html", "templates/evalrun.html")
	if err != nil {
		return nil, fmt.Errorf("parse eval run templates: %w", err)
	}
	diff, err := template.New("").Funcs(funcs).ParseFS(web.FS,
		"templates/base.html", "templates/error.html", "templates/diff.html")
	if err != nil {
		return nil, fmt.Errorf("parse diff templates: %w", err)
	}

	return &Handler{
		store:       st,
		listTmpl:    list,
		traceTmpl:   detail,
		partialTmpl: partials,
		evalsTmpl:   evals,
		evalRunTmpl: evalRun,
		diffTmpl:    diff,
	}, nil
}

// Routes registers the dashboard on mux.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /ui/", h.listPage)
	mux.HandleFunc("GET /ui/traces/{id}", h.tracePage)
	mux.HandleFunc("GET /ui/traces/{id}/spans/{spanID}", h.spanPanel)
	mux.HandleFunc("GET /ui/evals", h.evalsPage)
	mux.HandleFunc("GET /ui/evals/{id}", h.evalRunPage)
	mux.HandleFunc("GET /ui/diff", h.diffPage)
}

func (h *Handler) listPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	status := q.Get("status")
	if status != "ok" && status != "error" {
		status = ""
	}
	limit := 50
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	sums, err := h.store.ListTraces(r.Context(), store.TraceFilter{Status: status, Limit: limit})
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.render(w, h.listTmpl, "base", listView{
		Title:  "Traces",
		Status: status,
		Limit:  limit,
		Traces: newTraceRows(sums),
	})
}

func (h *Handler) tracePage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	spans, err := h.store.GetTrace(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		h.renderError(w, http.StatusNotFound, "trace not found")
		return
	}
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}

	options, err := h.diffOptions(r, id)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}

	nodes, start, end := buildNodes(spans)
	h.render(w, h.traceTmpl, "base", tracePageView{
		Title:       "Trace " + shortID(id),
		TraceID:     id,
		ShortID:     shortID(id),
		RootName:    rootName(spans),
		Status:      traceStatus(spans),
		Duration:    end.Sub(start),
		StartTime:   start,
		SpanCount:   len(spans),
		TotalCost:   totalCost(spans),
		Nodes:       nodes,
		DiffOptions: options,
	})
}

func (h *Handler) diffOptions(r *http.Request, excludeID string) ([]diffOption, error) {
	sums, err := h.store.ListTraces(r.Context(), store.TraceFilter{Limit: 100})
	if err != nil {
		return nil, err
	}
	options := make([]diffOption, 0, len(sums))
	for _, s := range sums {
		if s.TraceID == excludeID {
			continue
		}
		options = append(options, diffOption{
			TraceID:   s.TraceID,
			ShortID:   shortID(s.TraceID),
			RootName:  s.RootName,
			StartTime: s.StartTime,
		})
	}
	return options, nil
}

func (h *Handler) evalsPage(w http.ResponseWriter, r *http.Request) {
	runs, err := h.store.ListEvalRuns(r.Context(), store.EvalRunFilter{Limit: 200})
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.render(w, h.evalsTmpl, "base", evalsView{
		Title:  "Evals",
		Suites: newEvalSuiteRows(runs),
	})
}

func (h *Handler) evalRunPage(w http.ResponseWriter, r *http.Request) {
	run, err := h.store.GetEvalRun(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		h.renderError(w, http.StatusNotFound, "eval run not found")
		return
	}
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var cases []eval.CaseResult
	if len(run.Results) > 0 {
		if err := json.Unmarshal(run.Results, &cases); err != nil {
			h.renderError(w, http.StatusInternalServerError, "decode eval results: "+err.Error())
			return
		}
	}
	h.render(w, h.evalRunTmpl, "base", newEvalRunView(run, cases))
}

func (h *Handler) diffPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	leftID, rightID := q.Get("left"), q.Get("right")
	if leftID == "" || rightID == "" {
		h.renderError(w, http.StatusBadRequest, "diff requires left and right trace ids")
		return
	}

	left, err := h.store.GetTrace(r.Context(), leftID)
	if errors.Is(err, store.ErrNotFound) {
		h.renderError(w, http.StatusNotFound, "left trace not found")
		return
	}
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}
	right, err := h.store.GetTrace(r.Context(), rightID)
	if errors.Is(err, store.ErrNotFound) {
		h.renderError(w, http.StatusNotFound, "right trace not found")
		return
	}
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}

	rows, summary := alignSpans(left, right)
	h.render(w, h.diffTmpl, "base", diffView{
		Title:   "Diff " + shortID(leftID) + " → " + shortID(rightID),
		Left:    newDiffTraceHead(leftID, left),
		Right:   newDiffTraceHead(rightID, right),
		Summary: summary,
		Rows:    rows,
	})
}

func (h *Handler) spanPanel(w http.ResponseWriter, r *http.Request) {
	spans, err := h.store.GetTrace(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		h.renderError(w, http.StatusNotFound, "trace not found")
		return
	}
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, err.Error())
		return
	}

	spanID := r.PathValue("spanID")
	for _, s := range spans {
		if s.SpanID == spanID {
			h.render(w, h.partialTmpl, "span-panel", spanPanelView{
				Span:         s,
				Duration:     s.Duration(),
				KindClass:    kindClass(s.Kind),
				InputPretty:  jsonPretty(s.Input),
				OutputPretty: jsonPretty(s.Output),
			})
			return
		}
	}
	h.renderError(w, http.StatusNotFound, "span not found")
}

func (h *Handler) render(w http.ResponseWriter, tmpl *template.Template, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		// Headers are already written; nothing left to do but surface it.
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) renderError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = h.partialTmpl.ExecuteTemplate(w, "error", map[string]any{"Status": status, "Message": msg})
}

func jsonPretty(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}
