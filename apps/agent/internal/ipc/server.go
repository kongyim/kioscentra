package ipc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"sync"
	"time"
)

type pendingAction struct {
	Scope string `json:"scope"`
}

type Server struct {
	Snapshot    func(context.Context) (any, error)
	Action      func(string) error
	Audit       func(string)
	Technicians map[string]Technician
	mu          sync.Mutex
	hash        []byte
	nextLogin   time.Time
	grants      map[string]Grant
	nonces      map[string]time.Time
	now         func() time.Time
}

func NewServer(hash string) (*Server, error) {
	s := &Server{grants: map[string]Grant{}, nonces: map[string]time.Time{}, now: time.Now}
	if hash != "" {
		var e error
		s.hash, e = hex.DecodeString(hash)
		if e != nil || len(s.hash) != 32 {
			return nil, errors.New("maintenance SHA256 must be 64 hex characters")
		}
	}
	return s, nil
}
func (s *Server) acceptNonce(n string, issued time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if issued.Before(now.Add(-30*time.Second)) || issued.After(now.Add(5*time.Second)) {
		return false
	}
	for k, expiry := range s.nonces {
		if !expiry.After(now) {
			delete(s.nonces, k)
		}
	}
	if _, ok := s.nonces[n]; ok || len(s.nonces) >= 1024 {
		return false
	}
	s.nonces[n] = now.Add(35 * time.Second)
	return true
}
func allowed(scope string) bool {
	return scope == "refresh_inventory" || scope == "stop_service" || scope == "cancel_deployment"
}
func (s *Server) dispatch(ctx context.Context, r Request) (any, error) {
	switch r.Method {
	case "snapshot":
		return s.Snapshot(ctx)
	case "authorize":
		if !allowed(r.Scope) {
			return nil, errors.New("action is not allowed")
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		now := s.now()
		if !s.MaintenanceEnabled() {
			return nil, errors.New("maintenance authentication is not provisioned")
		}
		if now.Before(s.nextLogin) {
			return nil, errors.New("wait before another authentication attempt")
		}
		s.nextLogin = now.Add(5 * time.Second)
		expected := s.hash
		actor := "shared-development"
		scopeAllowed := len(s.Technicians) == 0
		if len(s.Technicians) != 0 {
			entry, ok := s.Technicians[r.Technician]
			expected, _ = hex.DecodeString(entry.SHA256)
			actor = "unknown"
			if ok {
				actor = r.Technician
			}
			for _, scope := range entry.Scopes {
				if scope == r.Scope {
					scopeAllowed = true
				}
			}
		}
		hash := sha256.Sum256([]byte(r.Secret))
		if subtle.ConstantTimeCompare(hash[:], expected) != 1 || !scopeAllowed {
			if s.Audit != nil {
				s.Audit("maintenance authentication failed: actor=" + actor)
			}
			return nil, errors.New("authentication failed")
		}
		for k, g := range s.grants {
			if !g.ExpiresAt.After(now) {
				delete(s.grants, k)
			}
		}
		if len(s.grants) >= 16 {
			return nil, errors.New("too many outstanding authorizations")
		}
		token := make([]byte, 32)
		if _, e := rand.Read(token); e != nil {
			return nil, e
		}
		g := Grant{Technician: actor, Token: base64.RawURLEncoding.EncodeToString(token), Scope: r.Scope, ExpiresAt: now.Add(2 * time.Minute)}
		s.grants[g.Token] = g
		if s.Audit != nil {
			s.Audit("maintenance authorization issued: " + r.Scope + " actor=" + actor)
		}
		return g, nil
	case "action":
		s.mu.Lock()
		g, ok := s.grants[r.Token]
		valid := ok && allowed(r.Scope) && g.Scope == r.Scope && g.ExpiresAt.After(s.now())
		if valid {
			delete(s.grants, r.Token)
		}
		s.mu.Unlock()
		if !valid {
			return nil, errors.New("authorization missing, expired, consumed or wrong scope")
		}
		if s.Audit != nil {
			s.Audit("maintenance action requested: " + r.Scope + " actor=" + g.Technician)
		}
		return pendingAction{Scope: r.Scope}, nil
	default:
		return nil, errors.New("unknown IPC method")
	}
}

// Serve bounds concurrency and closes every connection on cancellation.
func (s *Server) Serve(ctx context.Context, l net.Listener, key []byte) error {
	a, e := codec(key)
	if e != nil {
		return e
	}
	stop := context.AfterFunc(ctx, func() { l.Close() })
	defer stop()
	defer l.Close()
	slots := make(chan struct{}, 8)
	var workers sync.WaitGroup
	defer workers.Wait()
	for {
		c, e := l.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		select {
		case slots <- struct{}{}:
			workers.Add(1)
			go func() { defer workers.Done(); defer func() { <-slots }(); serveConnection(ctx, c, a, s) }()
		default:
			c.Close()
		}
	}
}
