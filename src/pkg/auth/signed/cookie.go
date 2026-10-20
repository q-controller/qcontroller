package signed

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"time"
)

// Cookie manages reading and writing signed, time-limited HTTP cookies.
type Cookie struct {
	Name   string
	Path   string
	TTL    time.Duration
	Key    []byte
	Secure bool
}

// Reader verifies the signed cookie from request and returns an io.Reader over its payload.
func (c Cookie) Reader(r *http.Request) (io.Reader, error) {
	hc, err := r.Cookie(c.Name)
	if err != nil {
		return nil, err
	}
	tok, err := FromString(hc.Value)
	if err != nil {
		return nil, err
	}
	payload, err := Verify(c.Key, tok)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(payload), nil
}

// Writer returns an io.WriteCloser that buffers the payload and sets the signed
// cookie on w when closed.
func (c Cookie) Writer(w http.ResponseWriter) io.WriteCloser {
	return &cookieWriter{cookie: c, w: w}
}

// Clear returns an expired cookie that instructs the client to delete it.
func (c Cookie) Clear() *http.Cookie {
	return c.newCookie("", -1)
}

func (c Cookie) newCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // G124: Secure follows the external_url scheme; a literal true breaks plain-HTTP dev
		Name:     c.Name,
		Value:    value,
		Path:     c.Path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   c.Secure,
		MaxAge:   maxAge,
	}
}

type cookieWriter struct {
	cookie Cookie
	buf    bytes.Buffer
	w      http.ResponseWriter
	closed bool
}

func (cw *cookieWriter) Write(p []byte) (int, error) {
	if cw.closed {
		return 0, os.ErrClosed
	}
	return cw.buf.Write(p)
}

func (cw *cookieWriter) Close() error {
	if cw.closed {
		return os.ErrClosed
	}
	cw.closed = true
	tok := Sign(cw.cookie.Key, cw.buf.Bytes(), cw.cookie.TTL)
	http.SetCookie(cw.w, cw.cookie.newCookie(tok.String(), int(cw.cookie.TTL.Seconds())))
	return nil
}
