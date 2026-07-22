package utils

import (
	"context"
	"io"
	"net/http"
	"sync"
)

// MultiHook handles Go method hooks, HTTP GET triggers, and HTTP POST triggers.
type MultiHook struct {
	mu             sync.RWMutex
	boolHooks      []func(bool)
	stringHooks    []func(string)
	intHooks       []func(int)
	interfaceHooks []func(interface{})

	// HTTP trigger hooks
	getHooks  []func()
	postHooks []func([]byte)

	httpServer *http.Server
}

// NewMultiHook creates a new MultiHook instance.
func NewMultiHook() *MultiHook {
	return &MultiHook{}
}

// OnBool registers a callback for bool triggers.
func (mh *MultiHook) OnBool(f func(bool)) {
	mh.mu.Lock()
	defer mh.mu.Unlock()
	mh.boolHooks = append(mh.boolHooks, f)
}

// TriggerBool invokes all registered bool hooks.
func (mh *MultiHook) TriggerBool(val bool) {
	mh.mu.RLock()
	defer mh.mu.RUnlock()
	for _, f := range mh.boolHooks {
		go f(val)
	}
}

// OnString registers a callback for string triggers.
func (mh *MultiHook) OnString(f func(string)) {
	mh.mu.Lock()
	defer mh.mu.Unlock()
	mh.stringHooks = append(mh.stringHooks, f)
}

// TriggerString invokes all registered string hooks.
func (mh *MultiHook) TriggerString(val string) {
	mh.mu.RLock()
	defer mh.mu.RUnlock()
	for _, f := range mh.stringHooks {
		go f(val)
	}
}

// OnInt registers a callback for int triggers.
func (mh *MultiHook) OnInt(f func(int)) {
	mh.mu.Lock()
	defer mh.mu.Unlock()
	mh.intHooks = append(mh.intHooks, f)
}

// TriggerInt invokes all registered int hooks.
func (mh *MultiHook) TriggerInt(val int) {
	mh.mu.RLock()
	defer mh.mu.RUnlock()
	for _, f := range mh.intHooks {
		go f(val)
	}
}

// OnInterface registers a callback for interface{} triggers.
func (mh *MultiHook) OnInterface(f func(interface{})) {
	mh.mu.Lock()
	defer mh.mu.Unlock()
	mh.interfaceHooks = append(mh.interfaceHooks, f)
}

// TriggerInterface invokes all registered interface{} hooks.
func (mh *MultiHook) TriggerInterface(val interface{}) {
	mh.mu.RLock()
	defer mh.mu.RUnlock()
	for _, f := range mh.interfaceHooks {
		go f(val)
	}
}

// OnGETTrigger registers a callback for HTTP GET triggers.
func (mh *MultiHook) OnGETTrigger(f func()) {
	mh.mu.Lock()
	defer mh.mu.Unlock()
	mh.getHooks = append(mh.getHooks, f)
}

// OnPOSTTrigger registers a callback for HTTP POST triggers.
func (mh *MultiHook) OnPOSTTrigger(f func([]byte)) {
	mh.mu.Lock()
	defer mh.mu.Unlock()
	mh.postHooks = append(mh.postHooks, f)
}

// StartHTTPServer starts an HTTP server on the given address.
// It exposes GET /trigger and POST /trigger endpoints.
func (mh *MultiHook) StartHTTPServer(addr string) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/trigger", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			mh.mu.RLock()
			for _, f := range mh.getHooks {
				go f()
			}
			mh.mu.RUnlock()

			w.WriteHeader(http.StatusOK)
			w.Write([]byte("GET trigger processed"))
			return
		}

		if r.Method == http.MethodPost {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Failed to read body", http.StatusBadRequest)
				return
			}

			mh.mu.RLock()
			for _, f := range mh.postHooks {
				go f(body)
			}
			mh.mu.RUnlock()

			w.Header().Set("Content-Type", r.Header.Get("Content-Type"))
			w.WriteHeader(http.StatusOK)
			w.Write(body)
			return
		}

		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	})

	mh.mu.Lock()
	mh.httpServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	mh.mu.Unlock()

	return mh.httpServer.ListenAndServe()
}

// StopHTTPServer stops the running HTTP server.
func (mh *MultiHook) StopHTTPServer(ctx context.Context) error {
	mh.mu.Lock()
	defer mh.mu.Unlock()
	if mh.httpServer != nil {
		return mh.httpServer.Shutdown(ctx)
	}
	return nil
}
