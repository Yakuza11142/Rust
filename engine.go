package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unsafe"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
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
	transportCache     = make(map[string]*http.Transport)
	transportCacheLock sync.Mutex
)

//export ExecuteStatelessScrape
func ExecuteStatelessScrape(cPayload *C.char) *C.char {
	goJSONString := C.GoString(cPayload)
	var payload InboundPayload

	if err := json.Unmarshal([]byte(goJSONString), &payload); err != nil {
		return serializeError("SYSTEM_ERROR", "JSON schema mismatch crossing C-boundary")
	}

	if payload.TimeoutSeconds <= 0 {
		payload.TimeoutSeconds = 15
	}

	if payload.RequestHeaders == nil {
		payload.RequestHeaders = make(map[string]string)
	}
	populateDefaultHeaders(payload.RequestHeaders)

	transport, err := getOrCreateTransport(payload.ProxyURL, payload.TimeoutSeconds)
	if err != nil {
		return serializeError("SYSTEM_ERROR", "Failed to initialize transport profile: "+err.Error())
	}

	client := &http.Client{
		Timeout:   time.Duration(payload.TimeoutSeconds) * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(payload.TimeoutSeconds)*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", payload.TargetURL, nil)
	if err != nil {
		return serializeError("SYSTEM_ERROR", "Runtime failure constructing context request")
	}

	for k, v := range payload.RequestHeaders {
		req.Header.Set(k, v)
	}

	if len(payload.Cookies) > 0 {
		var cookieSlice []string
		for k, v := range payload.Cookies {
			cookieSlice = append(cookieSlice, k+"="+v)
		}
		req.Header.Set("Cookie", strings.Join(cookieSlice, "; "))
	}

	resp, err := client.Do(req)
	if err != nil {
		return serializeError("EXECUTION_FAILURE", err.Error())
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return serializeError("STREAM_ERROR", "Failed to read response body stream")
	}

	respStatus := "SUCCESS"
	if resp.StatusCode == 403 || resp.StatusCode == 503 || resp.StatusCode == 401 {
		bodyStr := string(bodyBytes)
		if strings.Contains(bodyStr, "cf-chl-bypass") || strings.Contains(bodyStr, "challenges.cloudflare.com") || strings.Contains(bodyStr, "Turnstile") {
			respStatus = "CHALLENGE_DETECTED:CLOUDFLARE_MANAGED"
		} else if strings.Contains(bodyStr, "captcha") || strings.Contains(bodyStr, "recaptcha") || strings.Contains(bodyStr, "hcaptcha") {
			respStatus = "CHALLENGE_DETECTED:INTERACTIVE_CAPTCHA"
		} else {
			respStatus = "CHALLENGE_DETECTED:GENERIC_WAF"
		}
	}

	headers := make(map[string]string)
	for k, vals := range resp.Header {
		if len(vals) > 0 {
			headers[k] = vals[0]
		}
	}

	out := OutboundResponse{
		Status:     respStatus,
		StatusCode: resp.StatusCode,
		BodyBase64: base64.StdEncoding.EncodeToString(bodyBytes),
		Headers:    headers,
	}

	outBytes, _ := json.Marshal(out)
	return C.CString(string(outBytes))
}

func getOrCreateTransport(proxyURL string, timeoutSec int) (*http.Transport, error) {
	transportCacheLock.Lock()
	defer transportCacheLock.Unlock()

	cacheKey := proxyURL
	if t, exists := transportCache[cacheKey]; exists {
		return t, nil
	}

	dialer := &net.Dialer{
		Timeout:   time.Duration(timeoutSec) * time.Second,
		KeepAlive: 30 * time.Second,
	}

	var customDialer proxy.Dialer = dialer
	if proxyURL != "" {
		pURL, err := url.Parse(proxyURL)
		if err == nil {
			pd, err := proxy.FromURL(pURL, dialer)
			if err == nil {
				customDialer = pd
			}
		}
	}

	tr := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}

			rawConn, err := customDialer.Dial(network, addr)
			if err != nil {
				return nil, err
			}

			if tcpConn, ok := rawConn.(*net.TCPConn); ok {
				_ = tcpConn.SetKeepAlive(true)
				_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
			}

			uConn := utls.UClient(rawConn, &utls.Config{
				InsecureSkipVerify: false,
				ServerName:         host,
				NextProtos:         []string{"h2", "http/1.1"},
			}, utls.HelloChrome_120)

			if err := uConn.HandshakeContext(ctx); err != nil {
				rawConn.Close()
				return nil, err
			}
			return uConn, nil
		},
	}

	h2t, err := http2.ConfigureTransport(tr)
	if err == nil {
		h2t.InitialWindowSize = 6291456
		h2t.MaxHeaderListSize = 262144
	}

	transportCache[cacheKey] = tr
	return tr, nil
}

func populateDefaultHeaders(h map[string]string) {
	defaults := map[string]string{
		"User-Agent":                "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8",
		"Accept-Language":           "en-US,en;q=0.9",
		"Sec-Ch-Ua":                 `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`,
		"Sec-Ch-Ua-Mobile":          "?0",
		"Sec-Ch-Ua-Platform":        `"Windows"`,
		"Sec-Fetch-Dest":            "document",
		"Sec-Fetch-Mode":            "navigate",
		"Sec-Fetch-Site":            "none",
		"Sec-Fetch-User":            "?1",
		"Upgrade-Insecure-Requests": "1",
	}
	for k, v := range defaults {
		if _, ok := h[k]; !ok {
			h[k] = v
		}
	}
}

func serializeError(status, msg string) *C.char {
	res := OutboundResponse{Status: status, Error: msg}
	b, _ := json.Marshal(res)
	return C.CString(string(b))
}

//export ReleaseCMemory
func ReleaseCMemory(allocatedPointer *C.char) {
	C.free(unsafe.Pointer(allocatedPointer))
}

func main() {}
