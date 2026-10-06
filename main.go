package main

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// -----------------------------------------------------------------------------
// Configuration
// -----------------------------------------------------------------------------

type config struct {
	Port           string
	MetricsPath    string
	ScrapeInterval time.Duration
	Username       string
	Password       string
	BaseURL        string
}

func loadConfig() config {
	baseURL := getEnvOrDefault("GEMINITRACKER_BASE_URL", "https://gemini-tracker.org")
	return config{
		Port:           getEnvOrDefault("PORT", "9090"),
		MetricsPath:    getEnvOrDefault("METRICS_PATH", "/metrics"),
		Username:       os.Getenv("GEMINITRACKER_USERNAME"),
		Password:       os.Getenv("GEMINITRACKER_PASSWORD"),
		ScrapeInterval: parseDuration(os.Getenv("SCRAPE_INTERVAL"), 5*time.Minute),
		BaseURL:        strings.TrimSuffix(baseURL, "/"),
	}
}

func getEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// -----------------------------------------------------------------------------
// Site client (UNIT3D web session login + HTML profile scraping)
// -----------------------------------------------------------------------------

const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

var errSessionExpired = errors.New("session expired")

var (
	reToken   = regexp.MustCompile(`name="_token"\s+value="([^"]*)"`)
	reCaptcha = regexp.MustCompile(`name="_captcha"\s+value="([^"]*)"`)
	// Honeypot time field: hidden input with a random name, placed after _username.
	reTimeField = regexp.MustCompile(`<input type="hidden" name="([A-Za-z0-9]{8,})" value="(\d+)"`)
	reUploaded  = regexp.MustCompile(`(?s)ratio-bar__uploaded.*?</i>\s*([\d.,]+)\s*([A-Za-z]+)\s*</a>`)
	reDownload  = regexp.MustCompile(`(?s)ratio-bar__downloaded.*?</i>\s*([\d.,]+)\s*([A-Za-z]+)\s*</a>`)
)

type Client struct {
	mu            sync.Mutex
	authenticated bool
	baseURL       string
	username      string
	password      string
	httpClient    *http.Client
}

func newClient(baseURL, username, password string) *Client {
	c := &Client{baseURL: baseURL, username: username, password: password}
	c.resetHTTP()
	return c
}

func (c *Client) resetHTTP() {
	jar, _ := cookiejar.New(nil)
	c.httpClient = &http.Client{Timeout: 20 * time.Second, Jar: jar}
}

func (c *Client) IsAuthenticated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.authenticated
}

func (c *Client) do(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept-Language", "fr-FR,fr;q=0.9,en;q=0.8")
	return c.httpClient.Do(req)
}

// Login performs the UNIT3D form login: GET /login for CSRF/honeypot fields, then POST.
func (c *Client) Login() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loginLocked()
}

func (c *Client) loginLocked() error {
	c.authenticated = false
	c.resetHTTP()

	req, _ := http.NewRequest(http.MethodGet, c.baseURL+"/login", nil)
	req.Header.Set("Accept", "text/html")
	resp, err := c.do(req)
	if err != nil {
		return fmt.Errorf("login page request failed: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected login page response: %s", resp.Status)
	}
	html := string(body)

	tok := reToken.FindStringSubmatch(html)
	if tok == nil {
		return fmt.Errorf("CSRF _token not found on login page")
	}
	form := url.Values{}
	form.Set("_token", tok[1])
	form.Set("username", c.username)
	form.Set("password", c.password)
	form.Set("remember", "on")
	form.Set("_username", "")
	if m := reCaptcha.FindStringSubmatch(html); m != nil {
		form.Set("_captcha", m[1])
	}
	if m := reTimeField.FindStringSubmatch(html); m != nil {
		form.Set(m[1], m[2])
	}

	// The honeypot rejects forms submitted too fast.
	time.Sleep(4 * time.Second)

	// Do not follow the redirect: inspect where login sends us.
	noRedirect := *c.httpClient
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	post, _ := http.NewRequest(http.MethodPost, c.baseURL+"/login", strings.NewReader(form.Encode()))
	post.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	post.Header.Set("Referer", c.baseURL+"/login")
	post.Header.Set("Origin", c.baseURL)
	post.Header.Set("User-Agent", userAgent)
	resp, err = noRedirect.Do(post)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	loc := resp.Header.Get("Location")
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return fmt.Errorf("unexpected login response: %s", resp.Status)
	}
	if strings.Contains(loc, "/login") {
		return fmt.Errorf("login rejected (invalid credentials or honeypot), redirected to %s", loc)
	}
	if strings.Contains(loc, "two-factor") || strings.Contains(loc, "2fa") {
		return fmt.Errorf("2FA challenge required (%s): not supported", loc)
	}
	c.authenticated = true
	fmt.Printf("[auth] Successfully authenticated as %s\n", c.username)
	return nil
}

// FetchMetrics reads the profile page; re-logins once if the session expired.
func (c *Client) FetchMetrics() (*UserMetrics, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	m, err := c.fetchLocked()
	if errors.Is(err, errSessionExpired) {
		fmt.Println("[auth] Session expired, re-authenticating...")
		if lerr := c.loginLocked(); lerr != nil {
			return nil, fmt.Errorf("re-login failed: %w", lerr)
		}
		m, err = c.fetchLocked()
	}
	return m, err
}

func (c *Client) fetchLocked() (*UserMetrics, error) {
	req, _ := http.NewRequest(http.MethodGet, c.baseURL+"/users/"+url.PathEscape(c.username), nil)
	req.Header.Set("Accept", "text/html")
	resp, err := c.do(req)
	if err != nil {
		return nil, fmt.Errorf("profile request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.Request.URL.Path == "/login" || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == 419 {
		return nil, errSessionExpired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected profile response: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read profile response: %w", err)
	}
	return parseProfile(string(body))
}

// -----------------------------------------------------------------------------
// Domain types / parsing
// -----------------------------------------------------------------------------

type UserMetrics struct {
	Uploaded   float64
	Downloaded float64
}

func parseProfile(html string) (*UserMetrics, error) {
	up := reUploaded.FindStringSubmatch(html)
	down := reDownload.FindStringSubmatch(html)
	if up == nil || down == nil {
		return nil, fmt.Errorf("upload/download stats not found in profile page")
	}
	u, err := toBytes(up[1], up[2])
	if err != nil {
		return nil, err
	}
	d, err := toBytes(down[1], down[2])
	if err != nil {
		return nil, err
	}
	return &UserMetrics{Uploaded: u, Downloaded: d}, nil
}

// toBytes converts a UNIT3D human-readable size ("65.67 GiB") to bytes (1024-based).
func toBytes(num, unit string) (float64, error) {
	v, err := strconv.ParseFloat(strings.ReplaceAll(num, ",", ""), 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: %w", num, err)
	}
	pow := map[string]int{"B": 0, "KiB": 1, "MiB": 2, "GiB": 3, "TiB": 4, "PiB": 5, "EiB": 6,
		"KB": 1, "MB": 2, "GB": 3, "TB": 4, "PB": 5}
	p, ok := pow[unit]
	if !ok {
		return 0, fmt.Errorf("unknown size unit %q", unit)
	}
	for i := 0; i < p; i++ {
		v *= 1024
	}
	return v, nil
}

// -----------------------------------------------------------------------------
// Prometheus metrics
// -----------------------------------------------------------------------------

type exporterMetrics struct {
	totalUploaded   prometheus.Gauge
	totalDownloaded prometheus.Gauge
}

func newExporterMetrics(reg prometheus.Registerer) *exporterMetrics {
	m := &exporterMetrics{
		totalUploaded: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "geminitracker",
			Name:      "total_uploaded_bytes",
			Help:      "Total uploaded bytes from gemini-tracker.org",
		}),
		totalDownloaded: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "geminitracker",
			Name:      "total_downloaded_bytes",
			Help:      "Total downloaded bytes from gemini-tracker.org",
		}),
	}
	reg.MustRegister(m.totalUploaded, m.totalDownloaded)
	return m
}

func (m *exporterMetrics) update(u *UserMetrics) {
	m.totalUploaded.Set(u.Uploaded)
	m.totalDownloaded.Set(u.Downloaded)
}

// -----------------------------------------------------------------------------
// HTTP handlers
// -----------------------------------------------------------------------------

type server struct {
	client   *Client
	metrics  *exporterMetrics
	interval time.Duration

	mu        sync.Mutex
	lastFetch time.Time
}

func (s *server) metricsHandler(c *gin.Context) {
	s.mu.Lock()
	stale := time.Since(s.lastFetch) >= s.interval
	s.mu.Unlock()

	if stale && s.client.username != "" && s.client.password != "" {
		// Lazy (re)login if startup login failed.
		if !s.client.IsAuthenticated() {
			if err := s.client.Login(); err != nil {
				fmt.Printf("[auth] Login failed: %v\n", err)
			}
		}
		if s.client.IsAuthenticated() {
			userMetrics, err := s.client.FetchMetrics()
			if err != nil {
				fmt.Printf("[metrics] Error scraping metrics: %v\n", err)
			} else {
				s.metrics.update(userMetrics)
				s.mu.Lock()
				s.lastFetch = time.Now()
				s.mu.Unlock()
			}
		}
	}

	c.Header("Content-Type", "text/plain; version=0.0.4")
	promhttp.Handler().ServeHTTP(c.Writer, c.Request)
}

func (s *server) healthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":        "healthy",
		"authenticated": s.client.IsAuthenticated(),
	})
}

// -----------------------------------------------------------------------------
// Entrypoint
// -----------------------------------------------------------------------------

func main() {
	cfg := loadConfig()

	client := newClient(cfg.BaseURL, cfg.Username, cfg.Password)
	metrics := newExporterMetrics(prometheus.DefaultRegisterer)
	srv := &server{client: client, metrics: metrics, interval: cfg.ScrapeInterval}

	if cfg.Username != "" && cfg.Password != "" {
		fmt.Println("[auth] Credentials found, attempting auto-login...")
		if err := client.Login(); err != nil {
			fmt.Printf("[auth] Auto-login failed (non-fatal): %v\n", err)
		}
	} else {
		fmt.Println("[auth] No credentials provided, auto-login skipped")
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	r.GET(cfg.MetricsPath, srv.metricsHandler)
	r.GET("/health", srv.healthHandler)

	addr := ":" + cfg.Port
	fmt.Printf("[server] Starting geminitracker exporter on %s\n", addr)
	fmt.Printf("[server] Metrics available at: http://localhost%s%s\n", addr, cfg.MetricsPath)
	fmt.Printf("[server] Scrape interval: %v\n", cfg.ScrapeInterval)

	if err := r.Run(addr); err != nil {
		fmt.Printf("[server] Error starting server: %v\n", err)
		os.Exit(1)
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// parseDuration parses a simple duration string (e.g. "30s", "5m", "2h").
// Returns fallback if the string is empty or cannot be parsed.
func parseDuration(s string, fallback time.Duration) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	units := []struct {
		suffix string
		mult   time.Duration
	}{
		{"h", time.Hour},
		{"m", time.Minute},
		{"s", time.Second},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			numStr := strings.TrimSuffix(s, u.suffix)
			var n int
			if _, err := fmt.Sscanf(numStr, "%d", &n); err == nil && n > 0 {
				return time.Duration(n) * u.mult
			}
		}
	}
	return fallback
}
