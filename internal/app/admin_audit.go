package app

import (
	"net/http"
	"strconv"
)

func (a *App) adminAudit(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if s := r.URL.Query().Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 100 {
			problem(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = n
	}
	rows, err := a.DB.Query(`SELECT id,method,path,status_code,created_at
		FROM admin_audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		problem(w, http.StatusInternalServerError, "audit unavailable")
		return
	}
	reply(w, http.StatusOK, rows)
}
