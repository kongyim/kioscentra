package central

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kiosentra/packages/contracts"
)

// DeviceView deliberately excludes public keys, bearer tokens and token hashes.
type DeviceView struct {
	DeviceID            string  `json:"deviceId"`
	Hostname            string  `json:"hostname"`
	AgentVersion        string  `json:"agentVersion"`
	EnrolledAt          string  `json:"enrolledAt"`
	CredentialExpiresAt string  `json:"credentialExpiresAt"`
	LastSeenAt          *string `json:"lastSeenAt"`
	Online              bool    `json:"online"`
}
type DeviceDetail struct {
	DeviceView
	Heartbeat    json.RawMessage `json:"heartbeat"`
	Metrics      json.RawMessage `json:"metrics"`
	Inventory    json.RawMessage `json:"inventory"`
	PolicyReport json.RawMessage `json:"policyReport"`
}
type Report struct {
	Sequence   int64           `json:"sequence"`
	Path       string          `json:"path"`
	ReceivedAt string          `json:"receivedAt"`
	Body       json.RawMessage `json:"body"`
}
type DevicePage struct {
	Items      []DeviceView `json:"items"`
	NextCursor string       `json:"nextCursor"`
	Total      int          `json:"total"`
	Online     int          `json:"online"`
	Offline    int          `json:"offline"`
}
type ReportPage struct {
	Items      []Report `json:"items"`
	NextCursor string   `json:"nextCursor"`
}

const deviceColumns = "id,hostname,agent_version,enrolled_at,expires_at,last_seen_at"

type scanner interface{ Scan(...any) error }

func (s *Server) scanDevice(row scanner) (DeviceView, error) {
	var v DeviceView
	err := row.Scan(&v.DeviceID, &v.Hostname, &v.AgentVersion, &v.EnrolledAt, &v.CredentialExpiresAt, &v.LastSeenAt)
	if v.LastSeenAt != nil {
		last, e := time.Parse(time.RFC3339Nano, *v.LastSeenAt)
		v.Online = e == nil && !last.After(s.now()) && s.now().Sub(last) <= 90*time.Second
	}
	return v, err
}
func (s *Server) admin(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/v1/admin/jobs") {
		s.adminJobs(w, r)
		return
	}
	if r.Method != "GET" {
		fail(w, 405, "method_not_allowed")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/")
	if path == "devices" {
		s.devices(w, r)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[0] != "devices" || !contracts.ValidID(parts[1]) || len(parts) > 3 {
		fail(w, 404, "not_found")
		return
	}
	if len(parts) == 3 && parts[2] != "reports" {
		fail(w, 404, "not_found")
		return
	}
	v, err := s.scanDevice(s.db.QueryRowContext(r.Context(), "SELECT "+deviceColumns+" FROM devices WHERE id=?", parts[1]))
	if errors.Is(err, sql.ErrNoRows) {
		fail(w, 404, "device_not_found")
		return
	}
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	if len(parts) == 3 {
		s.reports(w, r, parts[1])
		return
	}
	detail := DeviceDetail{DeviceView: v}
	for path, dest := range map[string]*json.RawMessage{"/agent/heartbeat": &detail.Heartbeat, "/agent/metrics": &detail.Metrics, "/agent/inventory": &detail.Inventory, "/agent/policy/report": &detail.PolicyReport} {
		var body []byte
		err = s.db.QueryRowContext(r.Context(), "SELECT body FROM reports WHERE device_id=? AND path=? ORDER BY sequence DESC LIMIT 1", v.DeviceID, path).Scan(&body)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			fail(w, 503, "storage_unavailable")
			return
		}
		if err == nil {
			*dest = json.RawMessage(body)
		}
	}
	write(w, 200, detail)
}
func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	limit, err := pageLimit(r)
	if err != nil {
		fail(w, 400, "invalid_limit")
		return
	}
	cursor := r.URL.Query().Get("cursor")
	if cursor != "" && !contracts.ValidID(cursor) {
		fail(w, 400, "invalid_cursor")
		return
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	status := r.URL.Query().Get("status")
	if len(search) > 256 || (status != "" && status != "online" && status != "offline") {
		fail(w, 400, "invalid_filter")
		return
	}
	// Fixed SQL fragments only; all user values remain bound parameters.
	matching := "(instr(lower(hostname),lower(?))>0 OR instr(lower(id),lower(?))>0)"
	online := "(last_seen_at IS NOT NULL AND julianday(last_seen_at) BETWEEN julianday(?) AND julianday(?))"
	now := s.now()
	cutoff, current := now.Add(-90*time.Second).Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)
	page := DevicePage{Items: []DeviceView{}}
	if err = s.db.QueryRowContext(r.Context(), "SELECT COUNT(*),COALESCE(SUM(CASE WHEN "+online+" THEN 1 ELSE 0 END),0) FROM devices WHERE "+matching, cutoff, current, search, search).Scan(&page.Total, &page.Online); err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	page.Offline = page.Total - page.Online
	where := "id>? AND " + matching
	args := []any{cursor, search, search}
	if status != "" {
		if status == "offline" {
			where += " AND NOT " + online
		} else {
			where += " AND " + online
		}
		args = append(args, cutoff, current)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(r.Context(), "SELECT "+deviceColumns+" FROM devices WHERE "+where+" ORDER BY id LIMIT ?", args...)
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	defer rows.Close()
	for rows.Next() {
		v, e := s.scanDevice(rows)
		if e != nil {
			fail(w, 503, "storage_unavailable")
			return
		}
		if len(page.Items) == limit {
			page.NextCursor = page.Items[limit-1].DeviceID
			break
		}
		page.Items = append(page.Items, v)
	}
	if rows.Err() != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	write(w, 200, page)
}
func (s *Server) reports(w http.ResponseWriter, r *http.Request, id string) {
	limit, err := pageLimit(r)
	if err != nil {
		fail(w, 400, "invalid_limit")
		return
	}
	var cursor int64
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		cursor, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || cursor < 1 {
			fail(w, 400, "invalid_cursor")
			return
		}
	}
	order := r.URL.Query().Get("order")
	if order != "" && order != "asc" && order != "desc" {
		fail(w, 400, "invalid_order")
		return
	}
	where, direction := "device_id=?", "ASC"
	args := []any{id}
	if order == "desc" {
		direction = "DESC"
	}
	if cursor > 0 {
		if order == "desc" {
			where += " AND sequence<?"
		} else {
			where += " AND sequence>?"
		}
		args = append(args, cursor)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(r.Context(), "SELECT sequence,path,received_at,body FROM reports WHERE "+where+" ORDER BY sequence "+direction+" LIMIT ?", args...)
	if err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	defer rows.Close()
	page := ReportPage{Items: []Report{}}
	for rows.Next() {
		var v Report
		var body []byte
		if err = rows.Scan(&v.Sequence, &v.Path, &v.ReceivedAt, &body); err != nil {
			fail(w, 503, "storage_unavailable")
			return
		}
		if len(page.Items) == limit {
			page.NextCursor = strconv.FormatInt(page.Items[limit-1].Sequence, 10)
			break
		}
		v.Body = json.RawMessage(body)
		page.Items = append(page.Items, v)
	}
	if rows.Err() != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	write(w, 200, page)
}
