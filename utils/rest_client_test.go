package utils

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRESTClient_Get(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected MethodGet, got %s", r.Method)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("expected Authorization header, got %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Default") != "value" {
			t.Errorf("expected X-Default header, got %s", r.Header.Get("X-Default"))
		}
		if r.URL.Path != "/test-path" {
			t.Errorf("expected path /test-path, got %s", r.URL.Path)
		}
		if r.URL.Query().Get("foo") != "bar" {
			t.Errorf("expected query param foo=bar, got %s", r.URL.Query().Get("foo"))
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	client := NewRESTClient(
		WithBaseURL(server.URL),
		WithBearerAuth("test-token"),
		WithDefaultHeader("X-Default", "value"),
	)

	query := url.Values{}
	query.Set("foo", "bar")

	resp, err := client.Get(context.Background(), "/test-path", query, nil)
	if err != nil {
		t.Fatalf("failed to perform GET request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	if string(body) != `{"status":"ok"}` {
		t.Errorf("expected response body, got %s", string(body))
	}
}

func TestRESTClient_PostMultipart(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected MethodPost, got %s", r.Method)
		}

		err := r.ParseMultipartForm(10 << 20)
		if err != nil {
			t.Fatalf("failed to parse multipart form: %v", err)
		}

		if r.FormValue("key1") != "val1" {
			t.Errorf("expected key1=val1, got %s", r.FormValue("key1"))
		}

		file, header, err := r.FormFile("file1")
		if err != nil {
			t.Fatalf("failed to get form file: %v", err)
		}
		defer file.Close()

		if header.Filename != "test.txt" {
			t.Errorf("expected filename test.txt, got %s", header.Filename)
		}

		content, err := io.ReadAll(file)
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}
		if string(content) != "hello world" {
			t.Errorf("expected hello world content, got %s", string(content))
		}

		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := NewRESTClient(WithBaseURL(server.URL))

	fields := []MultipartField{
		{
			Name:  "key1",
			Value: "val1",
		},
		{
			Name:     "file1",
			FileName: "test.txt",
			Content:  strings.NewReader("hello world"),
		},
	}

	resp, err := client.PostMultipart(context.Background(), "/upload", nil, fields, nil)
	if err != nil {
		t.Fatalf("failed to perform PostMultipart: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected status 201, got %d", resp.StatusCode)
	}
}

func TestHTMLToMarkdown(t *testing.T) {
	htmlInput := `<html>
<head><title>Ignore me</title></head>
<body>
<h1>Heading 1</h1>
<p>This is a paragraph with <strong>bold</strong> and <em>italic</em> formatting.</p>
<a href="https://example.com">Example link</a>
<ul>
  <li>List item 1</li>
  <li>List item 2</li>
</ul>
</body>
</html>`

	expected := `# Heading 1

This is a paragraph with **bold** and *italic* formatting.

[Example link](https://example.com)

- List item 1
- List item 2`

	result := HTMLToMarkdown(htmlInput)
	if result != expected {
		t.Errorf("HTMLToMarkdown produced unexpected output:\nGOT:\n%q\nEXPECTED:\n%q", result, expected)
	}
}

func TestIsFileResponse(t *testing.T) {
	tests := []struct {
		headers  map[string]string
		expected bool
	}{
		{
			headers:  map[string]string{"Content-Disposition": "attachment; filename=archive.zip"},
			expected: true,
		},
		{
			headers:  map[string]string{"Content-Type": "image/jpeg"},
			expected: true,
		},
		{
			headers:  map[string]string{"Content-Type": "application/octet-stream"},
			expected: true,
		},
		{
			headers:  map[string]string{"Content-Type": "application/json"},
			expected: false,
		},
		{
			headers:  map[string]string{"Content-Type": "text/html; charset=utf-8"},
			expected: false,
		},
	}

	for i, tt := range tests {
		resp := &http.Response{Header: make(http.Header)}
		for k, v := range tt.headers {
			resp.Header.Set(k, v)
		}
		res := IsFileResponse(resp)
		if res != tt.expected {
			t.Errorf("test case %d failed: expected %v, got %v", i, tt.expected, res)
		}
	}
}

func TestDecodeToMarkdown(t *testing.T) {
	// JSON
	jsonResp := &http.Response{
		Header: http.Header{"Content-Type": []string{"application/json"}},
		Body:   io.NopCloser(strings.NewReader(`{"status":"success"}`)),
	}
	res, err := DecodeToMarkdown(jsonResp)
	if err != nil {
		t.Fatalf("JSON decode error: %v", err)
	}
	if !strings.Contains(res, "```json\n{\n  \"status\": \"success\"\n}\n```") {
		t.Errorf("expected pretty JSON block, got: %s", res)
	}

	// HTML
	htmlResp := &http.Response{
		Header: http.Header{"Content-Type": []string{"text/html"}},
		Body:   io.NopCloser(strings.NewReader(`<h1>Title</h1>`)),
	}
	res, err = DecodeToMarkdown(htmlResp)
	if err != nil {
		t.Fatalf("HTML decode error: %v", err)
	}
	if res != "# Title" {
		t.Errorf("expected MD heading, got: %q", res)
	}
}

func TestDownloadFile_Success(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "download-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	fileContent := "sample file bytes content"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", "attachment; filename=\"custom_archive.zip\"")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(fileContent))
	}))
	defer server.Close()

	client := NewRESTClient()
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET request failed: %v", err)
	}

	task, err := client.DownloadFile(context.Background(), resp, tempDir)
	if err != nil {
		t.Fatalf("DownloadFile failed to start: %v", err)
	}

	select {
	case result := <-task.ResultChan:
		if result.Error != nil {
			t.Fatalf("download task completed with error: %v", result.Error)
		}
		if result.FileInfo.FileName != "custom_archive.zip" {
			t.Errorf("expected file name custom_archive.zip, got %s", result.FileInfo.FileName)
		}
		if result.FileInfo.Size != int64(len(fileContent)) {
			t.Errorf("expected file size %d, got %d", len(fileContent), result.FileInfo.Size)
		}

		// Read and check content
		data, err := os.ReadFile(result.FileInfo.FilePath)
		if err != nil {
			t.Fatalf("failed to read downloaded file: %v", err)
		}
		if string(data) != fileContent {
			t.Errorf("expected content %q, got %q", fileContent, string(data))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("download timed out")
	}
}

func TestDownloadFile_Cancel(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "download-cancel-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		
		// Write chunks slowly to keep connection alive
		for i := 0; i < 100; i++ {
			w.Write([]byte("chunk of bytes..."))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer server.Close()

	client := NewRESTClient()
	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET request failed: %v", err)
	}

	task, err := client.DownloadFile(context.Background(), resp, tempDir)
	if err != nil {
		t.Fatalf("DownloadFile failed to start: %v", err)
	}

	// Trigger cancel after a short wait
	time.Sleep(50 * time.Millisecond)
	task.Cancel()

	select {
	case result := <-task.ResultChan:
		if result.Error == nil {
			t.Fatal("expected download to end with error, got nil")
		}
		if !strings.Contains(result.Error.Error(), "context canceled") {
			t.Errorf("expected error containing 'context canceled', got: %v", result.Error)
		}

		// Verify partial file was cleaned up/deleted
		files, err := os.ReadDir(tempDir)
		if err != nil {
			t.Fatalf("failed to read temp directory: %v", err)
		}
		if len(files) > 0 {
			t.Errorf("expected partial files to be cleaned up, but found %d files", len(files))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel verification timed out")
	}
}
