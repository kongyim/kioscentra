// Package ipc exposes a small, authenticated local service protocol, never HTTP.
package ipc

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"time"
)

const MaxResponse = 4 << 20
const maxRequest = 16 << 10

type Options struct {
	StateDir        string
	Development     bool
	ReaderSID       string
	Key             []byte
	MaintenanceHash string
}

func LoadOptions(dir string, development bool) (Options, error) {
	o := Options{StateDir: dir, Development: development, ReaderSID: os.Getenv("KIOSENTRA_UI_SID"), MaintenanceHash: os.Getenv("KIOSENTRA_MAINTENANCE_SHA256")}
	encoded := os.Getenv("KIOSENTRA_IPC_KEY")
	if encoded == "" {
		return o, nil
	}
	var e error
	o.Key, e = base64.StdEncoding.DecodeString(encoded)
	if e != nil || len(o.Key) != 32 {
		return o, errors.New("KIOSENTRA_IPC_KEY must encode exactly 32 random bytes")
	}
	return o, nil
}

type Request struct {
	Method   string    `json:"method"`
	Secret   string    `json:"secret"`
	Scope    string    `json:"scope"`
	Token    string    `json:"token"`
	IssuedAt time.Time `json:"issuedAt"`
}
type Response struct {
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
}
type Grant struct {
	Token     string    `json:"token"`
	Scope     string    `json:"scope"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func codec(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("local IPC is not configured")
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(block)
}
func send(w io.Writer, a cipher.AEAD, v any, ad string) ([]byte, error) {
	plain, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	if len(plain) > MaxResponse {
		return nil, errors.New("IPC response exceeds limit")
	}
	nonce := make([]byte, a.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return nil, e
	}
	data := append(append([]byte{}, nonce...), a.Seal(nil, nonce, plain, []byte(ad))...)
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))
	packet := append(header, data...)
	for len(packet) > 0 {
		n, e := w.Write(packet)
		if e != nil {
			return nil, e
		}
		if n == 0 {
			return nil, io.ErrShortWrite
		}
		packet = packet[n:]
	}
	return nonce, nil
}
func receive(r io.Reader, a cipher.AEAD, out any, ad string, limit int) ([]byte, error) {
	var header [4]byte
	if _, e := io.ReadFull(r, header[:]); e != nil {
		return nil, e
	}
	n := int(binary.BigEndian.Uint32(header[:]))
	if n < a.NonceSize()+a.Overhead() || n > limit+a.NonceSize()+a.Overhead() {
		return nil, errors.New("invalid IPC frame size")
	}
	data := make([]byte, n)
	if _, e := io.ReadFull(r, data); e != nil {
		return nil, e
	}
	nonce := data[:a.NonceSize()]
	plain, e := a.Open(nil, nonce, data[a.NonceSize():], []byte(ad))
	if e != nil {
		return nil, errors.New("IPC authentication failed")
	}
	if e = json.Unmarshal(plain, out); e != nil {
		return nil, e
	}
	return nonce, nil
}

type Client struct{ Options Options }

func (c Client) Call(ctx context.Context, r Request, out any) error {
	a, e := codec(c.Options.Key)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, e := dial(ctx, c.Options)
	if e != nil {
		return errors.New("service unavailable; waiting to reconnect")
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	r.IssuedAt = time.Now().UTC()
	nonce, e := send(conn, a, r, "kiosentra-request-v1")
	if e != nil {
		return e
	}
	var response Response
	if _, e = receive(conn, a, &response, "kiosentra-response-v1:"+base64.RawStdEncoding.EncodeToString(nonce), MaxResponse); e != nil {
		return e
	}
	if response.Error != "" {
		return errors.New(response.Error)
	}
	if out != nil {
		return json.Unmarshal(response.Data, out)
	}
	return nil
}
func serveConnection(ctx context.Context, conn net.Conn, a cipher.AEAD, s *Server) {
	defer conn.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	var r Request
	nonce, e := receive(conn, a, &r, "kiosentra-request-v1", maxRequest)
	if e != nil {
		return
	}
	if !s.acceptNonce(base64.RawStdEncoding.EncodeToString(nonce), r.IssuedAt) {
		return
	}
	data, e := s.dispatch(ctx, r)
	response := Response{Data: json.RawMessage("null")}
	if e != nil {
		response.Error = e.Error()
	} else {
		response.Data, _ = json.Marshal(data)
	}
	_, err := send(conn, a, response, "kiosentra-response-v1:"+base64.RawStdEncoding.EncodeToString(nonce))
	if action, ok := data.(pendingAction); ok && err == nil {
		if err = s.Action(action.Scope); err != nil && s.Audit != nil {
			s.Audit("maintenance action could not be queued")
		}
	}
}
