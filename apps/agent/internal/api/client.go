package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"kiosentra/packages/contracts"
)

type Client struct {
	base       string
	http       *http.Client
	credential contracts.Credential
}
type HTTPError struct {
	Status int
	Code   string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("API status %d (%s)", e.Status, e.Code) }
func New(base string, timeout time.Duration) *Client {
	return &Client{base: strings.TrimRight(base, "/") + "/api/v1", http: &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) SetCredential(v contracts.Credential) { c.credential = v }
func (c *Client) Do(ctx context.Context, method, path, id string, in, out any) error {
	var r io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return e
		}
		r = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if e != nil {
		return e
	}
	req.Header.Set("Content-Type", "application/json")
	if c.credential.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.credential.AccessToken)
	}
	if id != "" {
		req.Header.Set("Idempotency-Key", id)
	}
	resp, e := c.http.Do(req)
	if e != nil {
		return fmt.Errorf("API transport: %w", e)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var v contracts.Error
		_ = contracts.Decode(resp.Body, &v)
		return &HTTPError{resp.StatusCode, v.Code}
	}
	if out != nil {
		return contracts.Decode(resp.Body, out)
	}
	_, e = io.Copy(io.Discard, io.LimitReader(resp.Body, contracts.MaxBody))
	return e
}
func (c *Client) Enroll(ctx context.Context, v contracts.Enrollment) (contracts.Credential, error) {
	var out contracts.Credential
	e := c.Do(ctx, "POST", "/enrollments", "", v, &out)
	if e == nil && (!contracts.ValidID(out.DeviceID) || out.AccessToken == "" || !out.ExpiresAt.After(time.Now())) {
		e = fmt.Errorf("invalid enrollment response")
	}
	return out, e
}
func (c *Client) Rotate(ctx context.Context, id string, v contracts.Rotation) (contracts.Credential, error) {
	var out contracts.Credential
	e := c.Do(ctx, "POST", "/agent/credentials/rotate", id, v, &out)
	return out, e
}
func (c *Client) Heartbeat(ctx context.Context, id string, v contracts.Heartbeat) error {
	return c.Do(ctx, "POST", "/agent/heartbeat", id, v, nil)
}
func (c *Client) Metrics(ctx context.Context, id string, v contracts.Metrics) error {
	return c.Do(ctx, "POST", "/agent/metrics", id, v, nil)
}
func (c *Client) Inventory(ctx context.Context, id string, v contracts.Inventory) error {
	return c.Do(ctx, "PUT", "/agent/inventory", id, v, nil)
}
func (c *Client) Jobs(ctx context.Context, cursor string) (contracts.JobPage, error) {
	var out contracts.JobPage
	e := c.Do(ctx, "GET", "/agent/jobs?limit=100&cursor="+url.QueryEscape(cursor), "", nil, &out)
	return out, e
}
func (c *Client) Ack(ctx context.Context, id string, v contracts.Ack) error {
	return c.Do(ctx, "POST", "/agent/jobs/"+url.PathEscape(id)+"/ack", id, v, nil)
}
func (c *Client) Status(ctx context.Context, id, event string, v contracts.JobStatus) error {
	return c.Do(ctx, "POST", "/agent/jobs/"+url.PathEscape(id)+"/status", event, v, nil)
}
func (c *Client) Renew(ctx context.Context, id, event string, v contracts.Renew) (contracts.Lease, error) {
	var out contracts.Lease
	e := c.Do(ctx, "POST", "/agent/jobs/"+url.PathEscape(id)+"/renew", event, v, &out)
	return out, e
}
func (c *Client) Policy(ctx context.Context) (contracts.Policy, error) {
	var out contracts.Policy
	e := c.Do(ctx, "GET", "/agent/policy", "", nil, &out)
	return out, e
}
func (c *Client) ReportPolicy(ctx context.Context, id string, v contracts.PolicyReport) error {
	return c.Do(ctx, "POST", "/agent/policy/report", id, v, nil)
}
func (c *Client) LatestRelease(ctx context.Context) (*contracts.Release, error) {
	var out *contracts.Release
	e := c.Do(ctx, "GET", "/agent/releases/latest", "", nil, &out)
	return out, e
}
func (c *Client) UpdateStatus(ctx context.Context, id string, v contracts.UpdateStatus) error {
	return c.Do(ctx, "POST", "/agent/update-status", id, v, nil)
}
