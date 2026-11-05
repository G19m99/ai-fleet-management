package management

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory"
	log "github.com/sirupsen/logrus"
)

// usageHistoryResponse wraps usagehistory.Result with whether recording is on.
type usageHistoryResponse struct {
	Enabled bool `json:"enabled"`
	usagehistory.Result
}

// GetUsageHistory returns per-day usage rows between from and to.
//
// Query: from, to as YYYY-MM-DD (to exclusive; default: the current month),
// tz as an IANA zone such as "America/New_York" (default: server local time).
func (h *Handler) GetUsageHistory(c *gin.Context) {
	loc := time.Local
	if tz := strings.TrimSpace(c.Query("tz")); tz != "" {
		parsed, err := time.LoadLocation(tz)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tz"})
			return
		}
		loc = parsed
	}
	now := time.Now().In(loc)
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	to := from.AddDate(0, 1, 0)
	if v := strings.TrimSpace(c.Query("from")); v != "" {
		parsed, err := time.ParseInLocation("2006-01-02", v, loc)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid from, want YYYY-MM-DD"})
			return
		}
		from = parsed
	}
	if v := strings.TrimSpace(c.Query("to")); v != "" {
		parsed, err := time.ParseInLocation("2006-01-02", v, loc)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid to, want YYYY-MM-DD"})
			return
		}
		to = parsed
	}
	if !to.After(from) || to.Sub(from) > 400*24*time.Hour {
		c.JSON(http.StatusBadRequest, gin.H{"error": "range must be positive and at most 400 days"})
		return
	}

	dir := usagehistory.Default().Dir()
	resp := usageHistoryResponse{Enabled: dir != ""}
	if dir == "" {
		// Recording is off: still serve whatever was recorded earlier.
		h.mu.Lock()
		dir = usagehistory.PathFor(h.cfg)
		h.mu.Unlock()
	}
	if dir == "" {
		resp.Result = usagehistory.Result{Months: []string{}, Rows: []usagehistory.Row{}, TZ: loc.String(),
			From: from.Format("2006-01-02"), To: to.Format("2006-01-02")}
		c.JSON(http.StatusOK, resp)
		return
	}
	usagehistory.Flush()
	result, err := usagehistory.Query(dir, from, to, loc)
	if err != nil {
		log.WithError(err).Warn("usage history: query failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read usage history"})
		return
	}
	resp.Result = result
	c.JSON(http.StatusOK, resp)
}
