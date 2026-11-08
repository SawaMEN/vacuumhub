package app

import "net/http"

// Readiness checks the live SQLite connection (including a real query), not
// just whether the HTTP process is accepting requests.
func (a *App) readiness(w http.ResponseWriter, r *http.Request) {
 rows, err := a.DB.Query("SELECT 1 AS ready")
 if err != nil || len(rows) != 1 || rows[0]["ready"] != int64(1) {
  problem(w, http.StatusServiceUnavailable, "database unavailable")
  return
 }
 reply(w, http.StatusOK, map[string]string{"status":"ready"})
}
