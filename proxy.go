// Copyright (C) 2018 Betalo AB - All Rights Reserved

// Courtesy: https://medium.com/@mlowicki/http-s-proxy-in-golang-in-less-than-100-lines-of-code-6a51c2f2c38c

// $ openssl req -newkey rsa:2048 -nodes -keyout server.key -new -x509 -sha256 -days 3650 -out server.pem

package main

import (
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

// Proxy is a HTTPS forward proxy.
type Proxy struct {
	Logger             *zap.Logger
	AuthUser           string
	AuthPass           string
	DestDialTimeout    time.Duration
	DestReadTimeout    time.Duration
	DestWriteTimeout   time.Duration
	ClientReadTimeout  time.Duration
	ClientWriteTimeout time.Duration
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.Logger.Info("Incoming request", zap.String("host", r.Host))

	if r.Method != http.MethodConnect {
		p.Logger.Info("Method not allowed:", zap.String("method", r.Method))
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	if p.AuthUser != "" && p.AuthPass != "" {
		user, pass, ok := parseBasicProxyAuth(r.Header.Get("Proxy-Authenticate"))
		if !ok || user != p.AuthUser || pass != p.AuthPass {
			p.Logger.Warn("Authentication attempt with invalid credentials")
			http.Error(w, http.StatusText(http.StatusProxyAuthRequired), http.StatusProxyAuthRequired)
			return
		}
	}

	p.connect(w, r)
}

func (p *Proxy) connect(w http.ResponseWriter, r *http.Request) {
	p.Logger.Debug("Connecting:", zap.String("host", r.Host))

	destConn, err := net.DialTimeout("tcp", r.Host, p.DestDialTimeout)
	if err != nil {
		p.Logger.Error("Destination dial failed", zap.Error(err))
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	p.Logger.Debug("Connected", zap.String("host", r.Host))

	w.WriteHeader(http.StatusOK)

	p.Logger.Debug("Hijacking:", zap.String("host", r.Host))

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		p.Logger.Error("Hijacking not supported")
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		p.Logger.Error("Hijacking failed", zap.Error(err))
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}

	p.Logger.Debug("Hijacked connection", zap.String("host", r.Host))

	now := time.Now()
	clientConn.SetReadDeadline(now.Add(p.ClientReadTimeout))
	clientConn.SetWriteDeadline(now.Add(p.ClientWriteTimeout))
	destConn.SetReadDeadline(now.Add(p.DestReadTimeout))
	destConn.SetWriteDeadline(now.Add(p.DestWriteTimeout))

	go transfer(destConn, clientConn)
	go transfer(clientConn, destConn)
}

func transfer(dest io.WriteCloser, src io.ReadCloser) {
	defer func() { _ = dest.Close() }()
	defer func() { _ = src.Close() }()
	_, _ = io.Copy(dest, src)
}

// parseBasicProxyAuth parses an HTTP Basic Authentication string.
// "Basic QWxhZGRpbjpvcGVuIHNlc2FtZQ==" returns ("Aladdin", "open sesame", true).
func parseBasicProxyAuth(auth string) (username, password string, ok bool) {
	const prefix = "Basic "
	if !strings.HasPrefix(auth, prefix) {
		return
	}
	c, err := base64.StdEncoding.DecodeString(auth[len(prefix):])
	if err != nil {
		return
	}
	cs := string(c)
	s := strings.IndexByte(cs, ':')
	if s < 0 {
		return
	}
	return cs[:s], cs[s+1:], true
}