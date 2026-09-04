package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unsafe"

	utls "://github.com"
	"golang.org/x/net/proxy"
)

type InboundPayload struct {
	TargetURL      string            `json:"target_url"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	RequestHeaders map[string]string `json:"request_headers"`
	ProxyURL       string            `json:"proxy_url"`
	Cookies        map[string]string `json:"cookies"`
}

type OutboundResponse struct {
	Status     string            `json:"status"`
	StatusCode int               `json:"status_code"`
	BodyBase64 string            `json:"body_base64"`
	Headers    map[string]string `json:"headers"`
	Error      string            `json:"error,omitempty"`
}

var (
	sharedTransport *http.Transport
	transportOnce   sync.Once
)

func getSharedTransport() *http.Transport {
	transportOnce.Do(func() {
		sharedTransport = &http.Transport{
			MaxIdleConns: 500, MaxIdleConnsPerHost: 50, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
			Proxy: func(r *http.Request) (*url.URL, error) {
				if p, _ := r.Context().Value("p").(string); p != "" { return url.Parse(p) }
				return nil, nil
			},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil { host = addr }
				var rawConn net.Conn
				if pURLStr, _ := ctx.Value("p").(string); pURLStr != "" {
					if pURL, pErr := url.Parse(pURLStr); pErr == nil {
						switch pURL.Scheme {
						case "socks5", "socks5h":
							if d, dErr := proxy.FromURL(pURL, &net.Dialer{Timeout: 10 * time.Second}); dErr == nil {
								if cd, ok := d.(proxy.ContextDialer); ok { rawConn, err = cd.DialContext(ctx, network, addr) } else { rawConn, err = d.Dial(network, addr) }
							}
						case "http", "https":
							pAddr := pURL.Host
							if !strings.Contains(pAddr, ":") { pAddr += ":80" }
							if rawConn, err = (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, pAddr); err == nil {
								creq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", addr, addr)
								if pURL.User != nil { creq += fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", base64.StdEncoding.EncodeToString([]byte(pURL.User.String()))) }
								if _, err = rawConn.Write([]byte(creq + "\r\n")); err == nil {
									buf := make([]byte, 4096)
									if n, rErr := rawConn.Read(buf); rErr == nil && strings.Contains(string(buf[:n]), "200") {
										if idx := strings.Index(string(buf[:n]), "\r\n\r\n"); idx != -1 { break }
									} else { rawConn.Close(); return nil, fmt.Errorf("proxy connect failed") }
								}
							}
						}
					}
				}
				if rawConn == nil {
					if rawConn, err = (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, network, addr); err != nil { return nil, err }
				}
				uConn := utls.UClient(rawConn, &utls.Config{ServerName: host, NextProtos: []string{"http/1.1"}}, utls.HelloChrome_120)
				if err = uConn.HandshakeContext(ctx); err != nil { rawConn.Close(); return nil, err }
				return uConn, nil
			},
		}
	})
	return sharedTransport
}

//export ExecuteStatelessScrape
func ExecuteStatelessScrape(cPayload *C.char) *C.char {
	var payload InboundPayload
	if json.Unmarshal([]byte(C.GoString(cPayload)), &payload) != nil { return serializeError("SYSTEM_ERROR", "JSON boundary mismatch") }
	if payload.TimeoutSeconds <= 0 { payload.TimeoutSeconds = 15 }
	if payload.RequestHeaders == nil { payload.RequestHeaders = make(map[string]string) }

	defaults := map[string]string{
		"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8", "Accept-Language": "en-US,en;q=0.9",
		"Sec-Ch-Ua": `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`, "Sec-Ch-Ua-Mobile": "?0", "Sec-Ch-Ua-Platform": `"Windows"`,
		"Sec-Fetch-Dest": "document", "Sec-Fetch-Mode": "navigate", "Sec-Fetch-Site": "none", "Sec-Fetch-User": "?1", "Upgrade-Insecure-Requests": "1",
	}
	for k, v := range defaults {
		if _, ok := payload.RequestHeaders[k]; !ok { payload.RequestHeaders[k] = v }
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(payload.TimeoutSeconds)*time.Second)
	defer cancel()
	if payload.ProxyURL != "" { ctx = context.WithValue(ctx, "p", payload.ProxyURL) }

	req, err := http.NewRequestWithContext(ctx, "GET", payload.TargetURL, nil)
	if err != nil { return serializeError("SYSTEM_ERROR", "Request setup failure") }
	for k, v := range payload.RequestHeaders { req.Header.Set(k, v) }

	if len(payload.Cookies) > 0 {
		var cookies []string
		for k, v := range payload.Cookies { cookies = append(cookies, url.QueryEscape(k)+"="+url.QueryEscape(v)) }
		req.Header.Set("Cookie", strings.Join(cookies, "; "))
	}

	resp, err := (&http.Client{Timeout: time.Duration(payload.TimeoutSeconds) * time.Second, Transport: getSharedTransport()}).Do(req)
	if err != nil { return serializeError("EXECUTION_FAILURE", err.Error()) }
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil { return serializeError("STREAM_ERROR", "Stream failure") }

	status := "SUCCESS"
	if resp.StatusCode == 403 || resp.StatusCode == 503 || resp.StatusCode == 401 {
		bStr := string(bodyBytes)
		if strings.Contains(bStr, "cf-chl-bypass") || strings.Contains(bStr, "://cloudflare.com") || strings.Contains(bStr, "Turnstile") { 
			status = "CHALLENGE_DETECTED:CLOUDFLARE_MANAGED" 
		} else if strings.Contains(bStr, "captcha") || strings.Contains(bStr, "recaptcha") || strings.Contains(bStr, "hcaptcha") { 
			status = "CHALLENGE_DETECTED:INTERACTIVE_CAPTCHA" 
		} else { 
			status = "CHALLENGE_DETECTED:GENERIC_WAF" 
		}
	}

	headers := make(map[string]string)
	for k, vals := range resp.Header {
		if len(vals) > 0 { headers[k] = strings.Join(vals, ", ") }
	}

	out, _ := json.Marshal(OutboundResponse{Status: status, StatusCode: resp.StatusCode, BodyBase64: base64.StdEncoding.EncodeToString(bodyBytes), Headers: headers})
	return C.CString(string(out))
}

func serializeError(status, msg string) *C.char {
	b, _ := json.Marshal(OutboundResponse{Status: status, Error: msg})
	return C.CString(string(b))
}

//export ReleaseCMemory
func ReleaseCMemory(p *C.char) { C.free(unsafe.Pointer(p)) }

func main() {}
