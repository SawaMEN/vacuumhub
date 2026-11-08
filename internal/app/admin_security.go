package app

import (
	"crypto/subtle"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Bounded in-memory authentication failure limiter. No tokens or passwords
// are stored, and requests are never keyed on user-supplied forwarded headers
// unless the deployment explicitly trusts its *isolated* reverse proxy.
type authAttempts struct {
	mu      sync.Mutex
	records map[string]authAttempt
}
type authAttempt struct {
	failures int
	start    time.Time
}
const authWindow = 5 * time.Minute
const maxAuthFailures = 12

func (l *authAttempts) check(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if v, ok := l.records[key]; ok {
		if now.Sub(v.start) < authWindow && v.failures >= maxAuthFailures {
			return authWindow - now.Sub(v.start)
		}
		if now.Sub(v.start) >= authWindow {
			delete(l.records, key)
		}
	}
	return 0
}
func (l *authAttempts) failure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if l.records == nil { l.records = make(map[string]authAttempt) }
	v := l.records[key]
	if v.start.IsZero() || now.Sub(v.start) >= authWindow {
		v = authAttempt{start: now}
	}
	v.failures++
	l.records[key] = v
	if len(l.records) > 4096 {
		for k, record := range l.records {
			if now.Sub(record.start) >= authWindow {
				delete(l.records, k)
			}
		}
		if len(l.records) > 4096 {
			// Protect bounded memory under artificial IP rotation.
			l.records = map[string]authAttempt{key: v}
		}
	}
}
func (l *authAttempts) success(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.records, key)
}
func (a *App) authSource(r *http.Request) string {
	if a.TrustProxyHeaders {
		// Reverse proxy MUST overwrite X-Real-IP and prohibit direct app ingress.
		if ip := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil { return r.RemoteAddr }
	if ip := net.ParseIP(host); ip != nil { return ip.String() }
	return host
}

type statusWriter struct {
	http.ResponseWriter
	status int
}
func (w *statusWriter) WriteHeader(code int) {
	if w.status != 0 { return }
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
func (w *statusWriter) Write(p []byte) (int, error) {
	if w.status == 0 { w.WriteHeader(http.StatusOK) }
	return w.ResponseWriter.Write(p)
}
func (a *App) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		source := a.authSource(r)
		if retry := a.failures.check(source); retry > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
			problem(w, http.StatusTooManyRequests, "too many failed authentication attempts")
			return
		}
		if a.AdminUsername != "" {
			if !a.basicAuthenticated(r) {
				a.failures.failure(source)
				a.basicChallenge(w)
				return
			}
			if !nativeRequestAllowed(r) {
				problem(w, http.StatusForbidden, "admin request must originate from this site")
				return
			}
		} else {
			tokenHeader := r.Header.Get("X-Vacuumhub-Token")
			if tokenHeader != "" { tokenHeader = "Bearer " + tokenHeader } else { tokenHeader = r.Header.Get("Authorization") }
			if !validAdminToken(tokenHeader, a.AdminToken) {
				a.failures.failure(source)
				problem(w, http.StatusUnauthorized, "invalid admin token")
				return
			}
		}
		a.failures.success(source)
		if r.Method == http.MethodGet {
			next(w, r)
			return
		}
		rec := &statusWriter{ResponseWriter: w}
		next(rec, r)
		if rec.status == 0 { rec.status = http.StatusOK }
		// Best-effort audit, no secrets, body, hardware IDs, or IPs retained.
		path := r.URL.Path
		if len(path) > 250 { path = path[:250] }
		_ = a.DB.Exec(
			"INSERT INTO admin_audit(method,path,status_code) VALUES(?,?,?)",
			r.Method, path, rec.status,
		)
	}
}

func validAdminToken(header, configured string) bool {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || len(token) != len(configured) { return false }
	return subtle.ConstantTimeCompare([]byte(token), []byte(configured)) == 1
}
func readProxyConfig() bool {
	return strings.EqualFold(os.Getenv("TRUST_PROXY_HEADERS"), "true")
}
