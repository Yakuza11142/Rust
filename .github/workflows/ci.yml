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
	"time"
	"unsafe"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

type InboundPayload struct {
	TargetURL      string            `json:"target_url"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	RequestHeaders map[string]string `json:"request_headers"`
}

//export ExecuteStatelessScrape
func ExecuteStatelessScrape(cPayload *C.char) *C.char {
	goJSONString := C.GoString(cPayload)
	var payload InboundPayload

	if err := json.Unmarshal([]byte(goJSONString), &payload); err != nil {
		return C.CString("SYSTEM_ERROR: JSON schema mismatch crossing C-boundary")
	}

	if payload.TimeoutSeconds <= 0 {
		payload.TimeoutSeconds = 15
	}

	dialer := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			baseConn, err := net.DialTimeout(network, addr, time.Duration(payload.TimeoutSeconds)*time.Second)
			if err != nil {
				return nil, err
			}

			if tcpConn, ok := baseConn.(*net.TCPConn); ok {
				_ = tcpConn.SetKeepAlive(true)
				_ = tcpConn.SetKeepAlivePeriod(30 * time.Second)
			}

			uConn := utls.UClient(baseConn, &utls.Config{
				InsecureSkipVerify: false,
				ServerName:         addr,
			}, utls.HelloChrome_120)

			if err := uConn.HandshakeContext(ctx); err != nil {
				baseConn.Close()
				return nil, err
			}
			return uConn, nil
		},
	}

	if err := http2.ConfigureTransport(dialer); err != nil {
		return C.CString("SYSTEM_ERROR: Failed to configure HTTP/2 transport profile")
	}

	httpClient := &http.Client{
		Timeout:   time.Duration(payload.TimeoutSeconds) * time.Second,
		Transport: dialer,
	}

	req, err := http.NewRequestWithContext(context.Background(), "GET", payload.TargetURL, nil)
	if err != nil {
		return C.CString("SYSTEM_ERROR: Runtime failure constructing context request")
	}

	for headerKey, headerValue := range payload.RequestHeaders {
		req.Header.Set(headerKey, headerValue)
	}

	executionResponse, err := httpClient.Do(req)
	if err != nil {
		return C.CString("EXECUTION_FAILURE: Security firewall blocked negotiation profile: " + err.Error())
	}
	defer executionResponse.Body.Close()

	responseBuffer, err := io.ReadAll(executionResponse.Body)
	if err != nil {
		return C.CString("STREAM_ERROR: Failed to allocate output buffer stream")
	}

	return C.CString(string(responseBuffer))
}

//export ReleaseCMemory
func ReleaseCMemory(allocatedPointer *C.char) {
	C.free(unsafe.Pointer(allocatedPointer))
}

func main() {}
