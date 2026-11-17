// Package api serves the REST/SSE API and the embedded frontend.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/http"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"

	"lsd/internal/server/store"
	"lsd/internal/server/topology"
)

// ServerNodeID is the pseudo-node representing the server itself in the graph.
const ServerNodeID = "server"

type API struct {
	Store  *store.Store
	Live   *topology.Live
	Static fs.FS
	Log    *slog.Logger
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /api/nodes", a.handleNodes)
	mux.HandleFunc("GET /api/metrics", a.handleMetrics)
	mux.HandleFunc("GET /api/topology", a.handleTopology)
	mux.HandleFunc("GET /api/live", a.handleLive)
	mux.HandleFunc("GET /api/v1/query_range", a.handlePromQueryRange)
	mux.HandleFunc("GET /api/v1/query", a.handlePromQuery)
	mux.Handle("/", a.spa())
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func httpErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// ---- nodes -------------------------------------------------------------

type nodeView struct {
	store.Node
	Online bool          `json:"online"`
	Latest *store.Latest `json:"latest,omitempty"`
}

func (a *API) nodeViews(now time.Time) []nodeView {
	latest := a.Store.LatestAll()
	nodes := a.Store.Nodes.List()
	out := make([]nodeView, 0, len(nodes))
	for _, n := range nodes {
		v := nodeView{Node: n, Online: n.Online(now)}
		if l, ok := latest[n.ID]; ok {
			l := l
			v.Latest = &l
		}
		out = append(out, v)
	}
	return out
}

func (a *API) handleNodes(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, a.nodeViews(time.Now()))
}

// ---- time helpers ------------------------------------------------------

func parseTime(s string, def time.Time) (time.Time, error) {
	if s == "" {
		return def, nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		sec, frac := math.Modf(f)
		return time.Unix(int64(sec), int64(frac*1e9)), nil
	}
	return time.Parse(time.RFC3339Nano, s)
}

func parseDur(s string) (time.Duration, error) {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return time.Duration(f * float64(time.Second)), nil
	}
	d, err := model.ParseDuration(s)
	return time.Duration(d), err
}

// rangeParams resolves from/to/range/step with sane defaults and limits.
func rangeParams(r *http.Request) (from, to time.Time, step time.Duration, err error) {
	q := r.URL.Query()
	now := time.Now()
	to, err = parseTime(q.Get("to"), now)
	if err != nil {
		return
	}
	if rs := q.Get("range"); rs != "" {
		var d time.Duration
		if d, err = parseDur(rs); err != nil {
			return
		}
		from = to.Add(-d)
	} else if from, err = parseTime(q.Get("from"), to.Add(-time.Hour)); err != nil {
		return
	}
	if !from.Before(to) {
		err = fmt.Errorf("from must be before to")
		return
	}
	if to.Sub(from) > 400*24*time.Hour {
		err = fmt.Errorf("range too large")
		return
	}
	if ss := q.Get("step"); ss != "" {
		if step, err = parseDur(ss); err != nil {
			return
		}
	} else {
		step = autoStep(to.Sub(from))
	}
	if step < time.Second {
		step = time.Second
	}
	if int64(to.Sub(from)/step) > 11000 {
		step = to.Sub(from) / 10000
	}
	return
}

// autoStep targets ~360 points, rounded up to a multiple of 5s (min 5s).
func autoStep(rng time.Duration) time.Duration {
	s := time.Duration(math.Ceil(rng.Seconds()/360/5)) * 5 * time.Second
	if s < 5*time.Second {
		s = 5 * time.Second
	}
	return s
}

// ---- metrics -----------------------------------------------------------

type metricDef struct {
	query func(nm, w string, single bool) string
}

// nm is a ready-made node matcher (node=~"..."), w a range window like "15s".
var metricDefs = map[string]metricDef{
	"cpu": {func(nm, w string, _ bool) string {
		return fmt.Sprintf(`avg_over_time(lsd_cpu_percent{mode="total",%s}[%s])`, nm, w)
	}},
	"cpu_modes": {func(nm, w string, _ bool) string {
		return fmt.Sprintf(`avg_over_time(lsd_cpu_percent{mode=~"user|system|iowait",%s}[%s])`, nm, w)
	}},
	"load": {func(nm, w string, _ bool) string {
		return fmt.Sprintf(`avg_over_time(lsd_load{%s}[%s])`, nm, w)
	}},
	"mem_pct": {func(nm, w string, _ bool) string {
		return fmt.Sprintf(`100 * avg_over_time(lsd_mem_bytes{kind="used",%s}[%s]) / ignoring(kind) avg_over_time(lsd_mem_bytes{kind="total",%s}[%s])`, nm, w, nm, w)
	}},
	"mem_bytes": {func(nm, w string, _ bool) string {
		return fmt.Sprintf(`avg_over_time(lsd_mem_bytes{kind=~"used|cached|swap_used",%s}[%s])`, nm, w)
	}},
	// Swap used / total. clamp_min avoids 0/0 on machines without swap (they show 0%, not "no data").
	"swap_pct": {func(nm, w string, _ bool) string {
		return fmt.Sprintf(`100 * avg_over_time(lsd_mem_bytes{kind="swap_used",%s}[%s]) / ignoring(kind) clamp_min(avg_over_time(lsd_mem_bytes{kind="swap_total",%s}[%s]), 1)`, nm, w, nm, w)
	}},
	// Number of agents that pushed within the window (a single unlabeled series).
	"agents": {func(nm, w string, _ bool) string {
		return fmt.Sprintf(`count(count_over_time(lsd_node_up{%s}[%s]))`, nm, w)
	}},
	"disk_pct": {func(nm, w string, _ bool) string {
		return fmt.Sprintf(`100 * avg_over_time(lsd_disk_bytes{kind="used",%s}[%s]) / ignoring(kind) avg_over_time(lsd_disk_bytes{kind="total",%s}[%s])`, nm, w, nm, w)
	}},
	"disk_io": {func(nm, w string, single bool) string {
		by := "node,op"
		if single {
			by = "node,mount,op"
		}
		return fmt.Sprintf(`sum by (%s)(avg_over_time(lsd_disk_io_bps{%s}[%s]))`, by, nm, w)
	}},
	// Disk operations per second. All nodes: one total (read+write) line per node; single node: per filesystem and op.
	"disk_iops": {func(nm, w string, single bool) string {
		by := "node"
		if single {
			by = "node,mount,op"
		}
		return fmt.Sprintf(`sum by (%s)(avg_over_time(lsd_disk_iops{%s}[%s]))`, by, nm, w)
	}},
	"net_bps": {func(nm, w string, single bool) string {
		by := "node,dir"
		if single {
			by = "node,iface,dir"
		}
		return fmt.Sprintf(`sum by (%s)(avg_over_time(lsd_net_bps{%s}[%s]))`, by, nm, w)
	}},
	"net_pps": {func(nm, w string, single bool) string {
		by := "node,dir"
		if single {
			by = "node,iface,dir"
		}
		return fmt.Sprintf(`sum by (%s)(avg_over_time(lsd_net_pps{%s}[%s]))`, by, nm, w)
	}},
}

var nodeParamRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)

type series struct {
	Labels map[string]string `json:"labels"`
	Points [][2]float64      `json:"points"` // [unix ms, value]
}

func matrixToSeries(m promql.Matrix) []series {
	out := make([]series, 0, len(m))
	for _, s := range m {
		lbls := map[string]string{}
		s.Metric.Range(func(l labels.Label) {
			if l.Name != "__name__" {
				lbls[l.Name] = l.Value
			}
		})
		pts := make([][2]float64, 0, len(s.Floats))
		for _, p := range s.Floats {
			if math.IsNaN(p.F) || math.IsInf(p.F, 0) {
				continue
			}
			pts = append(pts, [2]float64{float64(p.T), p.F})
		}
		out = append(out, series{Labels: lbls, Points: pts})
	}
	sort.Slice(out, func(i, j int) bool { return fmt.Sprint(out[i].Labels) < fmt.Sprint(out[j].Labels) })
	return out
}

func (a *API) handleMetrics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	def, ok := metricDefs[q.Get("metric")]
	if !ok {
		keys := make([]string, 0, len(metricDefs))
		for k := range metricDefs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		httpErr(w, 400, "unknown metric; one of: "+strings.Join(keys, ", "))
		return
	}
	node := q.Get("node")
	if node == "" {
		node = "all"
	}
	nm := `node=~".+"`
	single := node != "all"
	if single {
		if !nodeParamRe.MatchString(node) {
			httpErr(w, 400, "invalid node")
			return
		}
		nm = fmt.Sprintf(`node=%s`, strconv.Quote(node))
	}
	from, to, step, err := rangeParams(r)
	if err != nil {
		httpErr(w, 400, err.Error())
		return
	}
	win := step
	if win < 15*time.Second {
		win = 15 * time.Second
	}
	query := def.query(nm, fmt.Sprintf("%ds", int(win.Seconds())), single)
	m, err := a.Store.QueryRange(r.Context(), query, from, to, step)
	if err != nil {
		httpErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"metric": q.Get("metric"), "node": node,
		"from": from.UnixMilli(), "to": to.UnixMilli(), "stepSeconds": step.Seconds(),
		"series": matrixToSeries(m),
	})
}

// ---- prometheus-compatible passthrough --------------------------------

func promError(w http.ResponseWriter, code int, typ string, err error) {
	writeJSON(w, code, map[string]string{"status": "error", "errorType": typ, "error": err.Error()})
}

func (a *API) handlePromQueryRange(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := time.Now()
	end, err := parseTime(q.Get("end"), now)
	if err != nil {
		promError(w, 400, "bad_data", err)
		return
	}
	start, err := parseTime(q.Get("start"), end.Add(-time.Hour))
	if err != nil {
		promError(w, 400, "bad_data", err)
		return
	}
	step := 15 * time.Second
	if s := q.Get("step"); s != "" {
		if step, err = parseDur(s); err != nil || step <= 0 {
			promError(w, 400, "bad_data", fmt.Errorf("invalid step"))
			return
		}
	}
	if end.Before(start) || int64(end.Sub(start)/step) > 11000 {
		promError(w, 400, "bad_data", fmt.Errorf("invalid range / too many points"))
		return
	}
	m, err := a.Store.QueryRange(r.Context(), q.Get("query"), start, end, step)
	if err != nil {
		promError(w, 422, "execution", err)
		return
	}
	writeJSON(w, 200, map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": m}})
}

func (a *API) handlePromQuery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ts, err := parseTime(q.Get("time"), time.Now())
	if err != nil {
		promError(w, 400, "bad_data", err)
		return
	}
	v, err := a.Store.Query(r.Context(), q.Get("query"), ts)
	if err != nil {
		promError(w, 422, "execution", err)
		return
	}
	writeJSON(w, 200, map[string]any{"status": "success", "data": map[string]any{"resultType": "vector", "result": v}})
}

// ---- topology ----------------------------------------------------------

type topoNode struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Kind    string `json:"kind"` // agent | server | external
	Online  bool   `json:"online"`
	Private bool   `json:"private"`
}

type topoEdge struct {
	Source string   `json:"source"`
	Target string   `json:"target"`
	Bps    float64  `json:"bps"`
	Pps    float64  `json:"pps"`
	Protos []string `json:"protos"`
}

type topoView struct {
	Mode        string     `json:"mode"` // live | history
	WindowSecs  float64    `json:"windowSeconds"`
	GeneratedAt int64      `json:"generatedAt"`
	Nodes       []topoNode `json:"nodes"`
	Edges       []topoEdge `json:"edges"`
}

func (a *API) buildTopology(mode string, window time.Duration, edges []topoEdge) topoView {
	now := time.Now()
	nodes := map[string]topoNode{}
	for _, n := range a.Store.Nodes.List() {
		nodes[n.ID] = topoNode{ID: n.ID, Label: n.Hostname, Kind: "agent", Online: n.Online(now)}
	}
	ensure := func(id string) {
		if _, ok := nodes[id]; ok {
			return
		}
		switch {
		case id == ServerNodeID:
			nodes[id] = topoNode{ID: id, Label: "server", Kind: "server", Online: true}
		case id == topology.ExternalOther:
			nodes[id] = topoNode{ID: id, Label: "other external", Kind: "external", Online: true}
		default:
			ip, err := netip.ParseAddr(id)
			nodes[id] = topoNode{ID: id, Label: id, Kind: "external", Online: true, Private: err == nil && (ip.IsPrivate() || ip.IsLinkLocalUnicast())}
		}
	}
	for _, e := range edges {
		ensure(e.Source)
		ensure(e.Target)
	}
	list := make([]topoNode, 0, len(nodes))
	for _, n := range nodes {
		list = append(list, n)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	if edges == nil {
		edges = []topoEdge{}
	}
	return topoView{Mode: mode, WindowSecs: window.Seconds(), GeneratedAt: now.UnixMilli(), Nodes: list, Edges: edges}
}

func (a *API) liveTopology(window time.Duration) topoView {
	snap := a.Live.Snapshot(time.Now(), window)
	edges := make([]topoEdge, 0, len(snap))
	for _, e := range snap {
		edges = append(edges, topoEdge{Source: e.Source, Target: e.Target, Bps: e.Bps, Pps: e.Pps, Protos: e.Protos})
	}
	return a.buildTopology("live", window, edges)
}

func (a *API) handleTopology(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("from") != "" || q.Get("range") != "" {
		from, to, _, err := rangeParams(r)
		if err != nil {
			httpErr(w, 400, err.Error())
			return
		}
		he, err := a.Store.FlowEdges(r.Context(), from, to)
		if err != nil {
			httpErr(w, 500, err.Error())
			return
		}
		edges := make([]topoEdge, 0, len(he))
		for _, e := range he {
			edges = append(edges, topoEdge{Source: e.Src, Target: e.Dst, Bps: e.Bps, Pps: e.Pps, Protos: uniqSorted(e.Protos)})
		}
		writeJSON(w, 200, a.buildTopology("history", to.Sub(from), edges))
		return
	}
	writeJSON(w, 200, a.liveTopology(windowParam(r)))
}

func uniqSorted(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func windowParam(r *http.Request) time.Duration {
	w := 30 * time.Second
	if s := r.URL.Query().Get("window"); s != "" {
		if d, err := parseDur(s); err == nil && d >= 5*time.Second && d <= 10*time.Minute {
			w = d
		}
	}
	return w
}

// ---- SSE ---------------------------------------------------------------

func (a *API) handleLive(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		httpErr(w, 500, "streaming unsupported")
		return
	}
	window := windowParam(r)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")

	send := func() bool {
		payload := map[string]any{"topology": a.liveTopology(window), "nodes": a.nodeViews(time.Now())}
		b, err := json.Marshal(payload)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", b); err != nil {
			return false
		}
		fl.Flush()
		return true
	}
	if !send() {
		return
	}
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			if !send() {
				return
			}
		}
	}
}

// ---- static SPA --------------------------------------------------------

func (a *API) spa() http.Handler {
	if a.Static == nil {
		return http.NotFoundHandler()
	}
	files := http.FS(a.Static)
	fileServer := http.FileServer(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if f, err := a.Static.Open(p); err == nil {
			f.Close()
			if strings.HasPrefix(p, "assets/") { // hashed filenames
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		// SPA fallback for client-side routes.
		r2 := r.Clone(context.Background())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}
