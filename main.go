package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var codeRegex = regexp.MustCompile(`^[a-zA-Z0-9]{6}$`)

// In-memory short-link store. Static, seeded once at startup.
var links = map[string]string{
	"prom01": "https://prometheus.io",
	"chrn01": "https://chronosphere.io",
	"goog01": "https://www.google.com",
	"gthb01": "https://github.com",
	"hckr01": "https://news.ycombinator.com",
	"wiki01": "https://en.wikipedia.org",
	"redt01": "https://www.reddit.com",
	"yutb01": "https://www.youtube.com",
	"mznn01": "https://www.amazon.com",
	"appl01": "https://www.apple.com",
	"mcsf01": "https://www.microsoft.com",
	"twtr01": "https://twitter.com",
	"lnkd01": "https://www.linkedin.com",
	"sofw01": "https://stackoverflow.com",
	"npmm01": "https://www.npmjs.com",
	"dckr01": "https://www.docker.com",
	"k8ss01": "https://kubernetes.io",
	"glng01": "https://go.dev",
	"rstt01": "https://www.rust-lang.org",
	"nflx01": "https://www.netflix.com",
}

var (
	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "resolver_request_duration_seconds",
		Help: "Duration of /resolve requests in seconds, partitioned by result.",
		// Custom buckets: in-memory map lookups are sub-millisecond, so the
		// default Prometheus buckets (starting at 5ms) would not produce reliable
		// results (I started with100 microseconds was way too high and always showing
		// 95 microseconds for p95, and 99 microseconds for p99, so had to lower this)
		Buckets: []float64{.000005, .00001, .00002, .00005, .0001, .0005, .001, .005, .01, .05, .1, .5, 1},

	}, []string{"result"})

	resolutionsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "resolver_resolutions_total",
		Help: "Total number of successful short-link resolutions.",
	})

	errorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "resolver_errors_total",
		Help: "Total number of failed resolutions, partitioned by application-level reason.",
	}, []string{"reason"})

	inflightRequests = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "resolver_inflight_requests",
		Help: "Number of /resolve requests currently being handled.",
	})
)

type resolveError struct {
	reason string
	status int
}

func (e *resolveError) Error() string { return e.reason }

var (
	errMissingCode = &resolveError{reason: "missing_code", status: http.StatusBadRequest}
	errBadFormat   = &resolveError{reason: "bad_format", status: http.StatusBadRequest}
	errNotFound    = &resolveError{reason: "not_found", status: http.StatusNotFound}
)

func resolve(code string) (string, error) {
	if code == "" {
		return "", errMissingCode
	}
	if !codeRegex.MatchString(code) {
		return "", errBadFormat
	}
	url, ok := links[code]
	if !ok {
		return "", errNotFound
	}
	return url, nil
}

func resolveHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		inflightRequests.Inc()
		defer inflightRequests.Dec()

		start := time.Now()
		if delayStr := r.URL.Query().Get("delay"); delayStr != "" {
		    if d, err := time.ParseDuration(delayStr); err == nil {
			time.Sleep(d)
		    }
		}
		code := r.URL.Query().Get("code")
		url, err := resolve(code)
		duration := time.Since(start)

		if err != nil {
			var rerr *resolveError
			if !errors.As(err, &rerr) { // unexpected error type — log it and return a generic 500
				logger.Error("unexpected error from resolve", "err", err)
			        http.Error(w, "internal error", http.StatusInternalServerError)
           			return
	        	}
			errorsTotal.WithLabelValues(rerr.reason).Inc()
			requestDuration.WithLabelValues("error").Observe(duration.Seconds())
			http.Error(w, rerr.reason, rerr.status)
			logger.Info("resolve",
				"code", code,
				"result", "error",
				"reason", rerr.reason,
				"duration_ms", duration.Milliseconds(),
			)
			return
		}

		resolutionsTotal.Inc()
		requestDuration.WithLabelValues("success").Observe(duration.Seconds())
		fmt.Fprintln(w, url)
		logger.Info("resolve",
			"code", code,
			"result", "success",
			"target_url", url,
			"duration_ms", duration.Milliseconds(),
		)
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	mux := http.NewServeMux()
	mux.HandleFunc("/resolve", resolveHandler(logger))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("server starting", "addr", srv.Addr, "seeded_codes", len(links))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("server shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
	}
}
