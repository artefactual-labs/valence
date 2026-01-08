package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/artefactual-labs/valence/internal/atomembed"
	"github.com/artefactual-labs/valence/internal/bootstrap"
)

const defaultAddr = ":8080"

type config struct {
	addr            string
	phpRoot         string
	frontController string
	atomDataDir     string
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("config error: %w", err)
	}

	bootstrapCfg, err := bootstrap.LoadConfigFromEnv(cfg.phpRoot)
	if err != nil {
		return fmt.Errorf("bootstrap config error: %w", err)
	}
	summary, err := bootstrap.Apply(bootstrapCfg)
	if err != nil {
		return fmt.Errorf("bootstrap error: %w", err)
	}
	log.Printf("bootstrap complete: wrote=%d skipped=%d", len(summary.Written), len(summary.Skipped))

	if err := waitForDependencies(); err != nil {
		return fmt.Errorf("dependency check failed: %w", err)
	}

	if err := runSymfonyPurge(cfg.phpRoot); err != nil {
		return fmt.Errorf("symfony purge failed: %w", err)
	}
	if err := runSymfonyCacheClear(cfg.phpRoot); err != nil {
		return fmt.Errorf("symfony cache clear failed: %w", err)
	}

	if err := initPHPRuntime(); err != nil {
		return fmt.Errorf("frankenphp init: %w", err)
	}
	defer shutdownPHPRuntime()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/metrics", metricsHandler)
	mux.HandleFunc("/.well-known/", wellKnownHandler)
	mux.HandleFunc("/v/storage/locations", storageLocationsHandler)
	mux.HandleFunc("/v/storage/locations/", storageLocationsHandler)
	mux.Handle("/", newAtomHandler(cfg))

	handler := withPermissionsPolicy(mux)

	srv := &http.Server{
		Addr:    cfg.addr,
		Handler: handler,
	}

	log.Printf("valence listening on %s", cfg.addr)
	return serveWithShutdown(srv)
}

func serveWithShutdown(srv *http.Server) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http listen: %w", err)
		}
		return nil
	case <-ctx.Done():
	}

	log.Printf("shutdown requested, stopping server")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown error: %v", err)
		_ = srv.Close()
	}

	err := <-errCh
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http listen: %w", err)
	}
	return nil
}

func loadConfig() (config, error) {
	addr := envOrDefault("VALENCE_ADDR", defaultAddr)
	absRoot, err := resolveAtomRoot()
	if err != nil {
		return config{}, err
	}
	atomDataDir := strings.TrimSpace(os.Getenv("ATOM_DATA_DIR"))
	if atomDataDir != "" {
		if abs, err := filepath.Abs(atomDataDir); err == nil {
			atomDataDir = abs
		}
	}
	frontController := filepath.Join(absRoot, "index.php")
	if info, err := os.Stat(frontController); err != nil || info.IsDir() {
		return config{}, fmt.Errorf("front controller not found at %s", frontController)
	}

	return config{
		addr:            addr,
		phpRoot:         absRoot,
		frontController: frontController,
		atomDataDir:     atomDataDir,
	}, nil
}

func envOrDefault(key, def string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return def
}

func envBool(key string, def bool) bool {
	val := strings.TrimSpace(os.Getenv(key))
	if val == "" {
		return def
	}
	parsed, err := strconv.ParseBool(val)
	if err != nil {
		return def
	}
	return parsed
}

func resolveAtomRoot() (string, error) {
	root := strings.TrimSpace(os.Getenv("VALENCE_ATOM_SRC_DIR"))
	if root == "" {
		return "", fmt.Errorf("VALENCE_ATOM_SRC_DIR is required")
	}

	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if err := ensureAtomRoot(abs); err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err == nil && info.IsDir() {
		return abs, nil
	}
	return "", fmt.Errorf("atom root not found at %s", abs)
}

func ensureAtomRoot(path string) error {
	forceExtract := envBool("VALENCE_ATOM_FORCE_EXTRACT", false)
	extracted, err := atomembed.EnsureExtracted(path, forceExtract)
	if err != nil {
		if errors.Is(err, atomembed.ErrAtomRootExists) {
			log.Printf("atom root exists at %s; skipping embedded extraction", path)
			return nil
		}
		return err
	}
	if extracted {
		log.Printf("extracted embedded atom archive to %s", path)
	}
	return nil
}

func waitForDependencies() error {
	mysqlDSN := strings.TrimSpace(os.Getenv("ATOM_MYSQL_DSN"))
	esHost := strings.TrimSpace(os.Getenv("ATOM_ELASTICSEARCH_HOST"))

	mysqlAddr, err := mysqlAddress(mysqlDSN)
	if err != nil {
		return fmt.Errorf("parse mysql dsn: %w", err)
	}
	esAddr, err := hostPort(esHost, 9200)
	if err != nil {
		return fmt.Errorf("parse elasticsearch host: %w", err)
	}

	if err := waitForTCP("mysql", mysqlAddr, 30, 2*time.Second); err != nil {
		return err
	}
	if err := waitForTCP("elasticsearch", esAddr, 30, 2*time.Second); err != nil {
		return err
	}
	return nil
}

func waitForTCP(name, addr string, attempts int, delay time.Duration) error {
	for i := 0; i < attempts; i++ {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err == nil {
			_ = conn.Close()
			log.Printf("%s reachable at %s", name, addr)
			return nil
		}
		log.Printf("%s not ready at %s (attempt %d/%d): %v", name, addr, i+1, attempts, err)
		time.Sleep(delay)
	}
	return fmt.Errorf("%s not reachable at %s after %d attempts", name, addr, attempts)
}

func mysqlAddress(dsn string) (string, error) {
	if dsn == "" {
		return "", fmt.Errorf("ATOM_MYSQL_DSN is empty")
	}
	trimmed := strings.TrimPrefix(dsn, "mysql:")
	parts := strings.Split(trimmed, ";")
	host := ""
	port := "3306"
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])
		switch key {
		case "host":
			host = value
		case "port":
			if value != "" {
				port = value
			}
		}
	}
	if host == "" {
		return "", fmt.Errorf("mysql host not found in dsn")
	}
	return net.JoinHostPort(host, port), nil
}

func hostPort(value string, defaultPort int) (string, error) {
	if value == "" {
		return "", fmt.Errorf("empty host")
	}
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil {
			return "", err
		}
		host := u.Hostname()
		port := u.Port()
		if port == "" {
			port = strconv.Itoa(defaultPort)
		}
		return net.JoinHostPort(host, port), nil
	}
	parts := strings.Split(value, ":")
	if len(parts) == 1 {
		return net.JoinHostPort(parts[0], strconv.Itoa(defaultPort)), nil
	}
	return net.JoinHostPort(parts[0], parts[1]), nil
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

func metricsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("metrics not implemented\n"))
}

func wellKnownHandler(w http.ResponseWriter, _ *http.Request) {
	http.NotFound(w, nil)
}

func withPermissionsPolicy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Allow legacy JS (e.g., YUI) to register unload handlers without browser warnings.
		// We can tighten this later if we remove those dependencies.
		w.Header().Set("Permissions-Policy", "unload=*")
		next.ServeHTTP(w, r)
	})
}

type atomHandler struct {
	phpRoot         string
	frontController string
	fallback        http.Handler
	atomDataDir     string
}

func newAtomHandler(cfg config) http.Handler {
	fallback := &frontControllerHandler{
		phpRoot:         cfg.phpRoot,
		frontController: cfg.frontController,
	}
	return &atomHandler{
		phpRoot:         cfg.phpRoot,
		frontController: cfg.frontController,
		fallback:        fallback,
		atomDataDir:     cfg.atomDataDir,
	}
}

func (h *atomHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqPath := cleanPath(r.URL.Path)
	if reqPath != r.URL.Path {
		clone := r.Clone(r.Context())
		clone.URL.Path = reqPath
		r = clone
	}

	if rewritten := stripLegacyFrontController(reqPath); rewritten != "" {
		clone := r.Clone(r.Context())
		clone.URL.Path = rewritten
		r = clone
		reqPath = rewritten
	}

	if rewritten := rewriteStoragePath(reqPath); rewritten != "" {
		clone := r.Clone(r.Context())
		clone.URL.Path = rewritten
		r = clone
		reqPath = rewritten
	}

	decision := h.decideRoute(r, reqPath)
	recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	decision.handler.ServeHTTP(recorder, r)
	logRouteDecision(r, decision.label, recorder.status, recorder.bytes)
}

func (h *atomHandler) staticAssetPath(requestPath string) (string, bool) {
	rel := strings.TrimPrefix(requestPath, "/")
	candidates := []string{}
	if h.atomDataDir != "" && downloadAssetRe.MatchString(requestPath) {
		candidates = append(candidates, filepath.Join(h.atomDataDir, filepath.FromSlash(rel)))
	}
	candidates = append(candidates, filepath.Join(h.phpRoot, filepath.FromSlash(rel)))

	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		return candidate, true
	}
	return "", false
}

func (h *atomHandler) existsOnDisk(requestPath string) bool {
	rel := strings.TrimPrefix(requestPath, "/")
	candidate := filepath.Join(h.phpRoot, filepath.FromSlash(rel))
	info, err := os.Stat(candidate)
	return err == nil && !info.IsDir()
}

func cleanPath(requestPath string) string {
	clean := path.Clean("/" + requestPath)
	if strings.Contains(clean, "..") {
		return "/"
	}
	return clean
}

func matchesStatic(reqPath string) bool {
	if staticAssetRe.MatchString(reqPath) {
		return true
	}
	if downloadAssetRe.MatchString(reqPath) {
		return true
	}
	return publicFileRe.MatchString(reqPath)
}

func stripLegacyFrontController(reqPath string) string {
	if strings.HasPrefix(reqPath, "/index.php/") {
		return "/" + strings.TrimPrefix(reqPath, "/index.php/")
	}
	if strings.HasPrefix(reqPath, "/qubit_dev.php/") {
		return "/" + strings.TrimPrefix(reqPath, "/qubit_dev.php/")
	}
	if reqPath == "/index.php" || reqPath == "/qubit_dev.php" {
		return "/"
	}
	return ""
}

func rewriteStoragePath(reqPath string) string {
	if reqPath == "/storage/location/list" {
		return "/storage/list"
	}
	return ""
}

type routeDecision struct {
	label   string
	handler http.Handler
}

func (h *atomHandler) decideRoute(r *http.Request, reqPath string) routeDecision {
	// Block internal paths.
	if privatePathRe.MatchString(reqPath) {
		return routeDecision{label: "deny_private", handler: http.NotFoundHandler()}
	}

	// Explicit deny-list for upload config directories.
	if uploadsConfRe.MatchString(reqPath) {
		return routeDecision{label: "deny_uploads_conf", handler: http.HandlerFunc(forbiddenHandler)}
	}

	// Explicit PHP entry points handled by PHP directly.
	if phpEntryRe.MatchString(reqPath) {
		return routeDecision{label: "php_entry", handler: h.fallback}
	}

	// Static assets served directly when they exist on disk.
	if matchesStatic(reqPath) {
		if assetPath, ok := h.staticAssetPath(reqPath); ok {
			return routeDecision{
				label: "static",
				handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					setStaticHeaders(w)
					http.ServeFile(w, r, assetPath)
				}),
			}
		}
		return routeDecision{label: "static_missing", handler: http.NotFoundHandler()}
	}

	// Uploaded artifacts are routed through the front controller.
	if uploadsAssetRe.MatchString(reqPath) {
		return routeDecision{label: "uploads_front_controller", handler: h.fallback}
	}

	// try_files $uri /index.php?$args; if the file exists, forbid direct access.
	if h.existsOnDisk(reqPath) {
		return routeDecision{label: "deny_direct_file", handler: http.HandlerFunc(forbiddenHandler)}
	}

	// Default: legacy Symfony front controller.
	return routeDecision{label: "front_controller", handler: h.fallback}
}

func setStaticHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("Expires", time.Now().Add(365*24*time.Hour).UTC().Format(http.TimeFormat))
}

func logRouteDecision(r *http.Request, decision string, status int, bytes int64) {
	if strings.TrimSpace(os.Getenv("VALENCE_LOG_ROUTES")) == "" {
		return
	}
	log.Printf("route=%s method=%s path=%s status=%d bytes=%d", decision, r.Method, r.URL.Path, status, bytes)
}

func forbiddenHandler(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "forbidden", http.StatusForbidden)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += int64(n)
	return n, err
}

var (
	staticAssetRe   = regexp.MustCompile(`^/(css|dist|js|images|plugins|vendor)/.*\.(css|png|jpg|js|svg|ico|gif|pdf|woff|woff2|otf|ttf)$`)
	downloadAssetRe = regexp.MustCompile(`^/(downloads)/.*\.(pdf|xml|html|csv|zip|rtf)$`)
	publicFileRe    = regexp.MustCompile(`^/(ead\.dtd|favicon\.ico|robots\.txt|sitemap.*)$`)
	uploadsConfRe   = regexp.MustCompile(`^/uploads/r/.*/conf/`)
	uploadsAssetRe  = regexp.MustCompile(`^/uploads/r/.*$`)
	privatePathRe   = regexp.MustCompile(`^/private/`)
	phpEntryRe      = regexp.MustCompile(`^/(index|qubit_dev)\.php(/|$)`)
)
