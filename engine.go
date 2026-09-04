package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"context"
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
)

type InboundPayload struct {
	TargetURL      string            `json:"target_url"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	RequestHeaders map[string]string `json:"request_headers"`
	ProxyURL       string            `json:"proxy_url"`
	Cookies        map[string]string `json:"cookies"`
}

type OutboundResponse struct {
	Status     string            `json:"status"` // "SUCCESS", "CHALLENGE_DETECTED", "SYSTEM_ERROR", "EXECUTION_FAILURE"
	StatusCode int               `json:"status_code"`
	Body       string            `json:"body"`
	Headers    map[string]string `json:"headers"`
	Error      string            `json:"error,omitempty"`
}

//export ExecuteStatelessScrape
func ExecuteStatelessScrape(cPayload *C.char) *C.char {
	goJSONString := C.GoString(cPayload)
	var payload InboundPayload

	if err := json.Unmarshal([]byte(goJSONString), &payload); err != nil {
		resp := OutboundResponse{
			Status: "SYSTEM_ERROR",
			Error:  "JSON schema mismatch crossing C-boundary",
		}
		bytes, _ := json.Marshal(resp)
		return C.CString(string(bytes))
	}

	if payload.TimeoutSeconds <= 0 {
		payload.TimeoutSeconds = 15
	}

	if payload.RequestHeaders == nil {
		payload.RequestHeaders = make(map[string]string)
	}
	if _, ok := payload.RequestHeaders["User-Agent"]; !ok {
		payload.RequestHeaders["User-Agent"] = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	}
	if _, ok := payload.RequestHeaders["Accept"]; !ok {
		payload.RequestHeaders["Accept"] = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8"
	}
	if _, ok := payload.RequestHeaders["Accept-Language"]; !ok {
		payload.RequestHeaders["Accept-Language"] = "en-US,en;q=0.9"
	}
	if _, ok := payload.RequestHeaders["Sec-Ch-Ua"]; !ok {
		payload.RequestHeaders["Sec-Ch-Ua"] = `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`
	}
	if _, ok := payload.RequestHeaders["Sec-Ch-Ua-Mobile"]; !ok {
		payload.RequestHeaders["Sec-Ch-Ua-Mobile"] = "?0"
	}
	if _, ok := payload.RequestHeaders["Sec-Ch-Ua-Platform"]; !ok {
		payload.RequestHeaders["Sec-Ch-Ua-Platform"] = `"Windows"`
	}
	if _, ok := payload.RequestHeaders["Sec-Fetch-Dest"]; !ok {
		payload.RequestHeaders["Sec-Fetch-Dest"] = "document"
	}
	if _, ok := payload.RequestHeaders["Sec-Fetch-Mode"]; !ok {
		payload.RequestHeaders["Sec-Fetch-Mode"] = "navigate"
	}
	if _, ok := payload.RequestHeaders["Sec-Fetch-Site"]; !ok {
		payload.RequestHeaders["Sec-Fetch-Site"] = "none"
	}
	if _, ok := payload.RequestHeaders["Sec-Fetch-User"]; !ok {
		payload.RequestHeaders["Sec-Fetch-User"] = "?1"
	}
	if _, ok := payload.RequestHeaders["Upgrade-Insecure-Requests"]; !ok {
		payload.RequestHeaders["Upgrade-Insecure-Requests"] = "1"
	}

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var baseConn net.Conn
			var err error

			if payload.ProxyURL != "" {
				proxyParsed, pErr := url.Parse(payload.ProxyURL)
				if pErr == nil && proxyParsed.Scheme == "http" {
					baseConn, err = net.DialTimeout(network, proxyParsed.Host, time.Duration(payload.TimeoutSeconds)*time.Second)
				} else {
					baseConn, err = net.DialTimeout(network, addr, time.Duration(payload.TimeoutSeconds)*time.Second)
				}
			} else {
				baseConn, err = net.DialTimeout(network, addr, time.Duration(payload.TimeoutSeconds)*time.Second)
			}

			if err != nil {
				return nil, err
			}

			if tcpConn, ok := baseConn.(*net.TCPConn); ok {
				_ = tcpConn.SetKeepAlive(true)
				_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
			}

			uConn := utls.UClient(baseConn, &utls.Config{
				InsecureSkipVerify: false,
				ServerName:         strings.Split(addr, ":")[0],
			}, utls.HelloChrome_120)

			if err := uConn.HandshakeContext(ctx); err != nil {
				baseConn.Close()
				return nil, err
			}
			return uConn, nil
		},
	}

	if payload.ProxyURL != "" {
		if pURL, err := url.Parse(payload.ProxyURL); err == nil && (pURL.Scheme == "socks5" || pURL.Scheme == "https") {
			transport.Proxy = http.ProxyURL(pURL)
		}
	}

	h2Transport, err := http2.ConfigureTransport(transport)
	if err != nil {
		resp := OutboundResponse{Status: "SYSTEM_ERROR", Error: "Failed to configure HTTP/2 transport profile"}
		bytes, _ := json.Marshal(resp)
		return C.CString(string(bytes))
	}

	h2Transport.InitialWindowSize = 6291456
	h2Transport.MaxHeaderListSize = 262144

	httpClient := &http.Client{
		Timeout:   time.Duration(payload.TimeoutSeconds) * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", payload.TargetURL, nil)
	if err != nil {
		resp := OutboundResponse{Status: "SYSTEM_ERROR", Error: "Runtime failure constructing context request"}
		bytes, _ := json.Marshal(resp)
		return C.CString(string(bytes))
	}

	for headerKey, headerValue := range payload.RequestHeaders {
		req.Header.Set(headerKey, headerValue)
	}

	if len(payload.Cookies) > 0 {
		var cookieSlice []string
		for k, v := range payload.Cookies {
			cookieSlice = append(cookieSlice, k+"="+v)
		}
		req.Header.Set("Cookie", strings.Join(cookieSlice, "; "))
	}

	executionResponse, err := httpClient.Do(req)
	if err != nil {
		resp := OutboundResponse{Status: "EXECUTION_FAILURE", Error: err.Error()}
		bytes, _ := json.Marshal(resp)
		return C.CString(string(bytes))
	}
	defer executionResponse.Body.Close()

	responseBuffer, err := io.ReadAll(executionResponse.Body)
	if err != nil {
		resp := OutboundResponse{Status: "STREAM_ERROR", Error: "Failed to allocate output buffer stream"}
		bytes, _ := json.Marshal(resp)
		return C.CString(string(bytes))
	}

	bodyStr := string(responseBuffer)
	respStatus := "SUCCESS"

	// Autonomous Bot Challenge Signature Detection Engine
	if executionResponse.StatusCode == 403 || executionResponse.StatusCode == 503 || executionResponse.StatusCode == 401 {
		if strings.Contains(bodyStr, "cf-chl-bypass") || strings.Contains(bodyStr, "challenges.cloudflare.com") || strings.Contains(bodyStr, "Turnstile") || strings.Contains(bodyStr, "just a moment") {
			respStatus = "CHALLENGE_DETECTED:CLOUDFLARE_MANAGED"
		} else if strings.Contains(bodyStr, "captcha") || strings.Contains(bodyStr, "recaptcha") || strings.Contains(bodyStr, "hcaptcha") {
			respStatus = "CHALLENGE_DETECTED:INTERACTIVE_CAPTCHA"
		} else if strings.Contains(bodyStr, "Akamai") || strings.Contains(bodyStr, "Incapsula") {
			respStatus = "CHALLENGE_DETECTED:AKAMAI_WAF"
		}
	}

	respHeaders := make(map[string]string)
	for k, vals := range executionResponse.Header {
		if len(vals) > 0 {
			respHeaders[k] = vals[0]
		}
	}

	finalOutput := OutboundResponse{
		Status:     respStatus,
		StatusCode: executionResponse.StatusCode,
		Body:       bodyStr,
		Headers:    respHeaders,
	}

	outBytes, _ := json.Marshal(finalOutput)
	return C.CString(string(outBytes))
}

//export ReleaseCMemory
func ReleaseCMemory(allocatedPointer *C.char) {
	C.free(unsafe.Pointer(allocatedPointer))
}

func main() {}
