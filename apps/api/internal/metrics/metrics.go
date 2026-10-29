// Package metrics exposes what the service is doing, in a format a monitoring system can scrape.
//
// It is deliberately hand-written rather than a Prometheus client library. The exposition format is
// small, the dependency tree of a client library is not, and this project's whole dependency surface is
// five modules that CI can fetch. What is *not* negotiable is the discipline, and that is what this
// package enforces:
//
//   - **Labels are chosen by the code, not by the caller's input.** A route label comes from chi's route
//     pattern, never from the request path: `/v1/orders/{id}` is one series, not one per order. An
//     unbounded label is how a monitoring system becomes the outage.
//   - **No identifier ever becomes a label.** There is no metric here with a user id, an email or an order
//     id in it. Metrics are scraped by a system that is not the API and is not covered by the API's
//     access controls.
//   - **A metric that is not registered is not emitted.** The alert rules are checked against the actual
//     exposition in CI, because a rule referencing a metric nobody emits is silently dead — it looks like
//     monitoring and alerts on nothing.
package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind is the Prometheus metric type.
type Kind string

const (
	KindCounter   Kind = "counter"
	KindGauge     Kind = "gauge"
	KindHistogram Kind = "histogram"
)

// DefaultBuckets cover the range this API actually lives in: a fast read is ~2 ms, a slow report is ~300
// ms, and anything past two seconds is a problem worth seeing on a graph rather than in a log line.
var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

// Registry holds every metric the process exposes. It is safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	families map[string]*family
	order    []string
	started  time.Time
}

type family struct {
	name   string
	help   string
	kind   Kind
	labels []string
	series map[string]*sample
	// buckets are only used by histograms.
	buckets []float64
}

type sample struct {
	values  []string
	value   float64 // counter/gauge
	sum     float64 // histogram
	count   uint64  // histogram
	buckets []uint64
}

// NewRegistry returns an empty registry that reports its own uptime from this moment.
func NewRegistry() *Registry {
	r := &Registry{families: map[string]*family{}, started: time.Now()}
	return r
}

// Handler is the handle a caller keeps. It is bound to one family and one label set.
type Handler struct {
	registry *Registry
	family   *family
	values   []string
}

func (r *Registry) declare(name, help string, kind Kind, labels []string, buckets []float64) *family {
	if f, ok := r.families[name]; ok {
		// Re-registering the same metric from two places would silently create two families with one
		// name in the exposition, which Prometheus rejects outright. Loud is better.
		if f.kind != kind || len(f.labels) != len(labels) {
			panic(fmt.Sprintf("metrics: %q registered twice with different shape", name))
		}
		return f
	}
	f := &family{name: name, help: help, kind: kind, labels: labels, series: map[string]*sample{}, buckets: buckets}
	r.families[name] = f
	r.order = append(r.order, name)
	return f
}

// Counter registers a monotonically increasing counter. Counters answer "how many", and are the only
// metric type that may be rate()d.
func (r *Registry) Counter(name, help string, labels ...string) *Handler {
	r.mu.Lock()
	defer r.mu.Unlock()
	return &Handler{registry: r, family: r.declare(name, help, KindCounter, labels, nil)}
}

// Gauge registers a value that can go up and down.
func (r *Registry) Gauge(name, help string, labels ...string) *Handler {
	r.mu.Lock()
	defer r.mu.Unlock()
	return &Handler{registry: r, family: r.declare(name, help, KindGauge, labels, nil)}
}

// Histogram registers a distribution. Observer values are in seconds.
func (r *Registry) Histogram(name, help string, labels ...string) *Handler {
	r.mu.Lock()
	defer r.mu.Unlock()
	return &Handler{registry: r, family: r.declare(name, help, KindHistogram, labels, DefaultBuckets)}
}

func (h *Handler) sample(values ...string) *sample {
	if len(values) != len(h.family.labels) {
		// A mismatch here is a programming error, and silently dropping the observation would make a
		// dashboard quietly wrong rather than visibly broken.
		panic(fmt.Sprintf("metrics: %q wants %d label value(s), got %d", h.family.name, len(h.family.labels), len(values)))
	}
	key := strings.Join(values, "\xff")
	s, ok := h.family.series[key]
	if !ok {
		s = &sample{values: append([]string(nil), values...)}
		if h.family.kind == KindHistogram {
			s.buckets = make([]uint64, len(h.family.buckets))
		}
		h.family.series[key] = s
	}
	return s
}

// Add increments a counter. Negative values are ignored: a counter that can go down is a gauge wearing
// the wrong label, and rate() over it produces nonsense.
func (h *Handler) Add(delta float64) {
	if delta < 0 {
		return
	}
	r := h.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	h.sample().value += delta
}

// Inc is Add(1).
func (h *Handler) Inc() { h.Add(1) }

// Labeled returns a handler bound to this label set, so call sites read as `requests.With("GET", route)`.
func (h *Handler) With(values ...string) Bound { return Bound{handler: h, values: values} }

// Bound is a handler with its label values already fixed.
type Bound struct {
	handler *Handler
	values  []string
}

// Add increments the bound series.
func (b Bound) Add(delta float64) {
	r := b.handler.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if delta < 0 {
		return
	}
	b.handler.sample(b.values...).value += delta
}

// Inc is Add(1).
func (b Bound) Inc() { b.Add(1) }

// Set replaces a gauge's value.
func (b Bound) Set(value float64) {
	r := b.handler.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	b.handler.sample(b.values...).value = value
}

// Observe records one histogram observation.
func (b Bound) Observe(value float64) {
	r := b.handler.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	s := b.handler.sample(b.values...)
	f := b.handler.family
	s.sum += value
	s.count++
	// Cumulative buckets, the way Prometheus expects them: each is "at or below this bound".
	for i, bound := range f.buckets {
		if value <= bound {
			s.buckets[i]++
		}
	}
}

// SetGauge sets a gauge that has no labels, for the common case.
func (h *Handler) Set(value float64) {
	r := h.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	h.sample().value = value
}

// Observe records one observation on an unlabeled histogram.
func (h *Handler) Observe(value float64) {
	r := h.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	s := h.sample()
	s.sum += value
	s.count++
	for i, bound := range h.family.buckets {
		if value <= bound {
			s.buckets[i]++
		}
	}
}

// Render writes the whole registry in the Prometheus text exposition format.
func (r *Registry) Render() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var out strings.Builder
	out.WriteString("# HELP edumart_process_uptime_seconds Seconds since this process started.\n")
	out.WriteString("# TYPE edumart_process_uptime_seconds gauge\n")
	out.WriteString(fmt.Sprintf("edumart_process_uptime_seconds %g\n", time.Since(r.started).Seconds()))

	for _, name := range r.order {
		f := r.families[name]
		out.WriteString(fmt.Sprintf("# HELP %s %s\n", name, escape(f.help)))
		out.WriteString(fmt.Sprintf("# TYPE %s %s\n", name, f.kind))
		keys := make([]string, 0, len(f.series))
		for key := range f.series {
			keys = append(keys, key)
		}
		sort.Strings(keys) // stable output makes a diff of two scrapes readable
		for _, key := range keys {
			s := f.series[key]
			switch f.kind {
			case KindHistogram:
				for i, bound := range f.buckets {
					out.WriteString(line(name+"_bucket", withExtra(f.labels, s.values, "le", formatFloat(bound)), float64(s.buckets[i])))
				}
				out.WriteString(line(name+"_bucket", withExtra(f.labels, s.values, "le", "+Inf"), float64(s.count)))
				out.WriteString(line(name+"_sum", pairs(f.labels, s.values), s.sum))
				out.WriteString(line(name+"_count", pairs(f.labels, s.values), float64(s.count)))
			default:
				out.WriteString(line(name, pairs(f.labels, s.values), s.value))
			}
		}
	}
	return out.String()
}

// ServeHTTP implements the scrape endpoint.
func (r *Registry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// Rendered into a string first: a scrape that fails halfway through leaves the monitoring system with
	// a truncated payload it may still parse, and a half-truth is worse than a failed scrape.
	_, _ = w.Write([]byte(r.Render()))
}

func line(name string, labels []string, value float64) string {
	if len(labels) == 0 {
		return fmt.Sprintf("%s %g\n", name, value)
	}
	return fmt.Sprintf("%s{%s} %g\n", name, strings.Join(labels, ","), value)
}

func pairs(names, values []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, len(names))
	for i := range names {
		out[i] = names[i] + "=\"" + escape(values[i]) + "\""
	}
	return out
}

func withExtra(names, values []string, extraName, extraValue string) []string {
	return append(pairs(names, values), extraName+"=\""+escape(extraValue)+"\"")
}

// escape implements the exposition format's escaping: backslash, newline and double quote only.
func escape(value string) string {
	var b strings.Builder
	for _, ch := range value {
		switch ch {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(ch)
		}
	}
	return b.String()
}

func formatFloat(value float64) string { return strconv.FormatFloat(value, 'g', -1, 64) }
