package relay

import (
	"bufio"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"time"
)

// metrics are counts only (SPEC.md §15).
type metrics struct {
	messages, created, joins, resumes, refusedPerIP, refusedTotal atomic.Int64
	errors                                                        [12]atomic.Int64 // indexed like errorCodes
}

func (m *metrics) countError(code string) {
	if i := slices.Index(errorCodes, code); i >= 0 {
		m.errors[i].Add(1)
	}
}

// metricsApps is how many <app> names /metrics labels: the day's top names in
// the usage record. Every other app counts as otherApp, so clients can't
// create label values at will.
const metricsApps = 50

// writeMetrics writes the Prometheus text format.
func (s *Server) writeMetrics(w io.Writer, version string) {
	named := s.usage.topApps(time.Now().UTC().Format(time.DateOnly), metricsApps)
	rooms := s.roomList()
	var connected, disconnected int
	byApp := make(map[string]int)
	for _, r := range rooms {
		r.mu.Lock()
		if !r.closed {
			for _, p := range r.participants {
				if p.conn != nil {
					connected++
				} else {
					disconnected++
				}
				app := clientName(p.client)
				if !named[app] {
					app = otherApp
				}
				byApp[app]++
			}
		}
		r.mu.Unlock()
	}
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	gauge := func(name, help string) {
		fmt.Fprintf(bw, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
	}
	counter := func(name, help string) {
		fmt.Fprintf(bw, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	}
	gauge("owp_build_info", "Relay build; the value is always 1.")
	fmt.Fprintf(bw, "owp_build_info{version=\"%s\"} 1\n", label(version))
	gauge("owp_connections", "Open WebSocket connections.")
	fmt.Fprintf(bw, "owp_connections %d\n", s.limits.connections())
	gauge("owp_rooms", "Rooms the relay holds.")
	fmt.Fprintf(bw, "owp_rooms %d\n", s.roomCount())
	gauge("owp_participants", "Participants in rooms, by whether a connection is attached.")
	fmt.Fprintf(bw, "owp_participants{connected=\"true\"} %d\nowp_participants{connected=\"false\"} %d\n",
		connected, disconnected)
	gauge("owp_participants_by_client", "Participants in rooms per <app> name of their client.")
	apps := make([]string, 0, len(byApp))
	for app := range byApp {
		apps = append(apps, app)
	}
	slices.Sort(apps)
	for _, app := range apps {
		fmt.Fprintf(bw, "owp_participants_by_client{client=\"%s\"} %d\n", label(app), byApp[app])
	}
	counter("owp_rooms_created_total", "Rooms created.")
	fmt.Fprintf(bw, "owp_rooms_created_total %d\n", s.metrics.created.Load())
	counter("owp_joins_total", "Accepted joins.")
	fmt.Fprintf(bw, "owp_joins_total %d\n", s.metrics.joins.Load())
	counter("owp_resumes_total", "Accepted resumes.")
	fmt.Fprintf(bw, "owp_resumes_total %d\n", s.metrics.resumes.Load())
	counter("owp_messages_total", "Inbound WebSocket data messages.")
	fmt.Fprintf(bw, "owp_messages_total %d\n", s.metrics.messages.Load())
	counter("owp_rejected_connections_total", "WebSocket upgrades refused by a connection limit.")
	fmt.Fprintf(bw, "owp_rejected_connections_total{reason=\"per_ip\"} %d\n", s.metrics.refusedPerIP.Load())
	fmt.Fprintf(bw, "owp_rejected_connections_total{reason=\"total\"} %d\n", s.metrics.refusedTotal.Load())
	counter("owp_errors_total", "error messages sent, by code.")
	for i, code := range errorCodes {
		fmt.Fprintf(bw, "owp_errors_total{code=\"%s\"} %d\n", code, s.metrics.errors[i].Load())
	}
}

func (s *Server) roomCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rooms)
}

// label escapes a Prometheus label value.
func label(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}
