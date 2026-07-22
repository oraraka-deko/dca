package utils

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func TestMultiHook_GoMethods(t *testing.T) {
	mh := NewMultiHook()

	boolChan := make(chan bool, 1)
	stringChan := make(chan string, 1)
	intChan := make(chan int, 1)
	interfaceChan := make(chan interface{}, 1)

	mh.OnBool(func(v bool) { boolChan <- v })
	mh.OnString(func(v string) { stringChan <- v })
	mh.OnInt(func(v int) { intChan <- v })
	mh.OnInterface(func(v interface{}) { interfaceChan <- v })

	mh.TriggerBool(true)
	mh.TriggerString("hello")
	mh.TriggerInt(42)
	mh.TriggerInterface(float64(3.14))

	select {
	case v := <-boolChan:
		if !v {
			t.Errorf("expected true, got %v", v)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("bool trigger timed out")
	}

	select {
	case v := <-stringChan:
		if v != "hello" {
			t.Errorf("expected hello, got %s", v)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("string trigger timed out")
	}

	select {
	case v := <-intChan:
		if v != 42 {
			t.Errorf("expected 42, got %d", v)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("int trigger timed out")
	}

	select {
	case v := <-interfaceChan:
		val, ok := v.(float64)
		if !ok || val != 3.14 {
			t.Errorf("expected float64 3.14, got %v", v)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("interface trigger timed out")
	}
}

func TestMultiHook_HTTPTriggers(t *testing.T) {
	port, err := getFreePort()
	if err != nil {
		t.Fatalf("failed to find a free port: %v", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	mh := NewMultiHook()

	getChan := make(chan struct{}, 1)
	postChan := make(chan string, 1)

	mh.OnGETTrigger(func() { getChan <- struct{}{} })
	mh.OnPOSTTrigger(func(body []byte) { postChan <- string(body) })

	// Start server in goroutine
	go func() {
		_ = mh.StartHTTPServer(addr)
	}()

	// Allow server to boot up
	time.Sleep(100 * time.Millisecond)

	baseURL := fmt.Sprintf("http://%s/trigger", addr)

	// 1. Test GET /trigger
	resp, err := http.Get(baseURL)
	if err != nil {
		t.Fatalf("GET request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	select {
	case <-getChan:
		// success
	case <-time.After(1 * time.Second):
		t.Fatal("GET trigger hook was not called")
	}

	// 2. Test POST /trigger
	postBody := "payload-data"
	resp2, err := http.Post(baseURL, "text/plain", bytes.NewReader([]byte(postBody)))
	if err != nil {
		t.Fatalf("POST request failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp2.StatusCode)
	}

	bodyBytes, err := io.ReadAll(resp2.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	if string(bodyBytes) != postBody {
		t.Errorf("expected returned request body %q, got %q", postBody, string(bodyBytes))
	}

	select {
	case v := <-postChan:
		if v != postBody {
			t.Errorf("expected hook body %q, got %q", postBody, v)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("POST trigger hook was not called")
	}

	// Stop server
	err = mh.StopHTTPServer(context.Background())
	if err != nil {
		t.Errorf("failed to shutdown HTTP server: %v", err)
	}
}
