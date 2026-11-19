package ipc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"
)

// RPC uses the existing authenticated encrypted frame transport with its own
// protocol domain. Requests are short submissions/queries, never long executions.
type RPCClient struct {
	Options Options
	Domain  string
}
type rpcRequest struct {
	IssuedAt time.Time       `json:"issuedAt"`
	Payload  json.RawMessage `json:"payload"`
}

func (c RPCClient) Call(ctx context.Context, in, out any) error {
	if c.Domain == "" {
		return errors.New("RPC domain required")
	}
	a, err := codec(c.Options.Key)
	if err != nil {
		return err
	}
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	if len(b) > 60<<10 {
		return errors.New("RPC request too large")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := dial(ctx, c.Options)
	if err != nil {
		return errors.New("local broker unavailable")
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	nonce, err := send(conn, a, rpcRequest{IssuedAt: time.Now().UTC(), Payload: b}, c.Domain+":request")
	if err != nil {
		return err
	}
	var response Response
	if _, err = receive(conn, a, &response, c.Domain+":response:"+base64.RawStdEncoding.EncodeToString(nonce), 64<<10); err != nil {
		return err
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	if out != nil {
		return json.Unmarshal(response.Data, out)
	}
	return nil
}

func ServeRPC(ctx context.Context, l net.Listener, key []byte, domain string, handler func(context.Context, json.RawMessage) (any, error)) error {
	if domain == "" || handler == nil {
		l.Close()
		return errors.New("RPC domain and handler required")
	}
	a, err := codec(key)
	if err != nil {
		l.Close()
		return err
	}
	guard, _ := NewServer("")
	stop := context.AfterFunc(ctx, func() { l.Close() })
	defer stop()
	defer l.Close()
	slots := make(chan struct{}, 8)
	var workers sync.WaitGroup
	defer workers.Wait()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-slots }()
			defer conn.Close()
			requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
			stop := context.AfterFunc(requestCtx, func() { conn.Close() })
			defer stop()
			var r rpcRequest
			nonce, err := receive(conn, a, &r, domain+":request", 64<<10)
			if err != nil {
				return
			}
			if !guard.acceptNonce(base64.RawStdEncoding.EncodeToString(nonce), r.IssuedAt) {
				return
			}
			value, err := handler(requestCtx, r.Payload)
			response := Response{}
			if err != nil {
				response.Error = err.Error()
			} else {
				response.Data, err = json.Marshal(value)
				if err != nil {
					response.Error = "RPC response unavailable"
				}
			}
			_, _ = send(conn, a, response, domain+":response:"+base64.RawStdEncoding.EncodeToString(nonce))
		}()
	}
}
