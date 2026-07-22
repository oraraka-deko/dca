package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Azure/go-autorest/autorest"
)

// FileInfo holds information about a downloaded file.
type FileInfo struct {
	FileName    string `json:"filename"`
	FilePath    string `json:"filepath"`
	Size        int64  `json:"size"`
	ContentType string `json:"content_type"`
}

// DownloadResult holds the final result of a file download task.
type DownloadResult struct {
	FileInfo FileInfo
	Error    error
}

// DownloadTask represents an ongoing file download task.
type DownloadTask struct {
	cancelFunc context.CancelFunc
	ResultChan <-chan DownloadResult
}

// Cancel cancels the ongoing download task.
func (dt *DownloadTask) Cancel() {
	if dt.cancelFunc != nil {
		dt.cancelFunc()
	}
}

// RESTClient wraps autorest.Client to provide a programmatic REST interface.
type RESTClient struct {
	client  autorest.Client
	baseURL string
	headers map[string]string
}

// RESTClientOption defines configuration functions for the client.
type RESTClientOption func(*RESTClient)

// WithBaseURL configures the base URL for RESTClient.
func WithBaseURL(urlStr string) RESTClientOption {
	return func(c *RESTClient) {
		c.baseURL = strings.TrimSuffix(urlStr, "/")
	}
}

// WithUserAgent configures a custom User-Agent.
func WithUserAgent(ua string) RESTClientOption {
	return func(c *RESTClient) {
		c.client.AddToUserAgent(ua)
	}
}

// WithSender sets the sender for autorest pipeline.
func WithSender(sender autorest.Sender) RESTClientOption {
	return func(c *RESTClient) {
		c.client.Sender = sender
	}
}

// WithAuthorizer sets the authorizer (e.g. bearer, basic, or custom autorest.Authorizer).
func WithAuthorizer(auth autorest.Authorizer) RESTClientOption {
	return func(c *RESTClient) {
		c.client.Authorizer = auth
	}
}

// WithDefaultHeader adds a default header to all requests.
func WithDefaultHeader(key, value string) RESTClientOption {
	return func(c *RESTClient) {
		if c.headers == nil {
			c.headers = make(map[string]string)
		}
		c.headers[key] = value
	}
}

// HeaderAuthorizer is an autorest.Authorizer that adds a custom header for authentication.
type HeaderAuthorizer struct {
	HeaderName  string
	HeaderValue string
}

// WithAuthorization implements autorest.Authorizer interface.
func (ha HeaderAuthorizer) WithAuthorization() autorest.PrepareDecorator {
	return func(p autorest.Preparer) autorest.Preparer {
		return autorest.PreparerFunc(func(r *http.Request) (*http.Request, error) {
			r, err := p.Prepare(r)
			if err != nil {
				return r, err
			}
			return autorest.Prepare(r, autorest.WithHeader(ha.HeaderName, ha.HeaderValue))
		})
	}
}

// NewAPIKeyAuthorizer creates a HeaderAuthorizer for API keys (e.g. X-API-Key).
func NewAPIKeyAuthorizer(name, value string) autorest.Authorizer {
	return HeaderAuthorizer{HeaderName: name, HeaderValue: value}
}

// NewSimpleBearerAuthorizer creates a HeaderAuthorizer for static bearer tokens.
func NewSimpleBearerAuthorizer(token string) autorest.Authorizer {
	if !strings.HasPrefix(token, "Bearer ") {
		token = "Bearer " + token
	}
	return HeaderAuthorizer{HeaderName: "Authorization", HeaderValue: token}
}

// WithAPIKeyAuth configures API Key authorization for the client.
func WithAPIKeyAuth(name, value string) RESTClientOption {
	return func(c *RESTClient) {
		c.client.Authorizer = NewAPIKeyAuthorizer(name, value)
	}
}

// WithBearerAuth configures Bearer Token authorization for the client.
func WithBearerAuth(token string) RESTClientOption {
	return func(c *RESTClient) {
		c.client.Authorizer = NewSimpleBearerAuthorizer(token)
	}
}

// NewRESTClient creates a new RESTClient with optional configurations.
func NewRESTClient(opts ...RESTClientOption) *RESTClient {
	rc := &RESTClient{
		client:  autorest.NewClientWithUserAgent("dca-rest-client/1.0"),
		headers: make(map[string]string),
	}
	for _, opt := range opts {
		opt(rc)
	}
	return rc
}

// Client returns the underlying autorest.Client.
func (c *RESTClient) Client() *autorest.Client {
	return &c.client
}

// SetAuthorizer updates the client's Authorizer.
func (c *RESTClient) SetAuthorizer(auth autorest.Authorizer) {
	c.client.Authorizer = auth
}

// Get performs a GET request.
func (c *RESTClient) Get(ctx context.Context, path string, query url.Values, headers map[string]string) (*http.Response, error) {
	return c.Send(ctx, http.MethodGet, path, query, nil, headers)
}

// Post performs a POST request.
func (c *RESTClient) Post(ctx context.Context, path string, query url.Values, body interface{}, headers map[string]string) (*http.Response, error) {
	return c.Send(ctx, http.MethodPost, path, query, body, headers)
}

// Put performs a PUT request.
func (c *RESTClient) Put(ctx context.Context, path string, query url.Values, body interface{}, headers map[string]string) (*http.Response, error) {
	return c.Send(ctx, http.MethodPut, path, query, body, headers)
}

// Delete performs a DELETE request.
func (c *RESTClient) Delete(ctx context.Context, path string, query url.Values, headers map[string]string) (*http.Response, error) {
	return c.Send(ctx, http.MethodDelete, path, query, nil, headers)
}

// MultipartField represents a field in a multipart form request.
type MultipartField struct {
	Name     string
	Value    string
	FileName string
	Content  io.Reader
}

// PostMultipart sends a POST request with multipart/form-data body.
func (c *RESTClient) PostMultipart(ctx context.Context, path string, query url.Values, fields []MultipartField, headers map[string]string) (*http.Response, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for _, field := range fields {
		if field.Content != nil {
			part, err := writer.CreateFormFile(field.Name, field.FileName)
			if err != nil {
				return nil, fmt.Errorf("failed to create form file field %q: %w", field.Name, err)
			}
			_, err = io.Copy(part, field.Content)
			if err != nil {
				return nil, fmt.Errorf("failed to copy file content for field %q: %w", field.Name, err)
			}
		} else {
			err := writer.WriteField(field.Name, field.Value)
			if err != nil {
				return nil, fmt.Errorf("failed to write text field %q: %w", field.Name, err)
			}
		}
	}

	err := writer.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to close multipart writer: %w", err)
	}

	if headers == nil {
		headers = make(map[string]string)
	}
	headers["Content-Type"] = writer.FormDataContentType()

	return c.Send(ctx, http.MethodPost, path, query, body, headers)
}

// WithBody is a PrepareDecorator that sets the request body to the given io.Reader.
func WithBody(bodyReader io.Reader) autorest.PrepareDecorator {
	return func(p autorest.Preparer) autorest.Preparer {
		return autorest.PreparerFunc(func(r *http.Request) (*http.Request, error) {
			r, err := p.Prepare(r)
			if err != nil {
				return r, err
			}
			if bodyReader == nil {
				return r, nil
			}

			var length int64
			switch l := bodyReader.(type) {
			case *bytes.Buffer:
				length = int64(l.Len())
			case *bytes.Reader:
				length = int64(l.Len())
			case *strings.Reader:
				length = int64(l.Len())
			}

			rc, ok := bodyReader.(io.ReadCloser)
			if !ok {
				rc = io.NopCloser(bodyReader)
			}
			r.Body = rc
			if length > 0 {
				r.ContentLength = length
			}
			return r, nil
		})
	}
}

func mapToInterface(m map[string]string) map[string]interface{} {
	res := make(map[string]interface{}, len(m))
	for k, v := range m {
		res[k] = v
	}
	return res
}

func queryToInterface(q url.Values) map[string]interface{} {
	res := make(map[string]interface{}, len(q))
	for k, v := range q {
		if len(v) == 1 {
			res[k] = v[0]
		} else {
			res[k] = v
		}
	}
	return res
}

// Send prepares and sends an HTTP request using the autorest request pipeline.
func (c *RESTClient) Send(ctx context.Context, method, path string, query url.Values, body interface{}, headers map[string]string) (*http.Response, error) {
	var bodyReader io.Reader
	var isJSON bool

	if body != nil {
		switch b := body.(type) {
		case io.Reader:
			bodyReader = b
		case []byte:
			bodyReader = bytes.NewReader(b)
		case string:
			bodyReader = strings.NewReader(b)
		default:
			jsonData, err := json.Marshal(body)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal request body to JSON: %w", err)
			}
			bodyReader = bytes.NewReader(jsonData)
			isJSON = true
		}
	}

	var preparers []autorest.PrepareDecorator

	switch strings.ToUpper(method) {
	case http.MethodGet:
		preparers = append(preparers, autorest.AsGet())
	case http.MethodPost:
		preparers = append(preparers, autorest.AsPost())
	case http.MethodPut:
		preparers = append(preparers, autorest.AsPut())
	case http.MethodDelete:
		preparers = append(preparers, autorest.AsDelete())
	case http.MethodPatch:
		preparers = append(preparers, autorest.AsPatch())
	default:
		preparers = append(preparers, autorest.AsPost())
	}

	fullURL := path
	if c.baseURL != "" {
		if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
			fullURL = c.baseURL + "/" + strings.TrimPrefix(path, "/")
		}
	}
	preparers = append(preparers, autorest.WithBaseURL(fullURL))

	if len(query) > 0 {
		preparers = append(preparers, autorest.WithQueryParameters(queryToInterface(query)))
	}

	if bodyReader != nil {
		preparers = append(preparers, WithBody(bodyReader))
	}

	allHeaders := make(map[string]string)
	for k, v := range c.headers {
		allHeaders[k] = v
	}
	for k, v := range headers {
		allHeaders[k] = v
	}
	if isJSON && allHeaders["Content-Type"] == "" {
		allHeaders["Content-Type"] = "application/json"
	}

	if len(allHeaders) > 0 {
		preparers = append(preparers, autorest.WithHeaders(mapToInterface(allHeaders)))
	}

	req, err := autorest.Prepare(&http.Request{}, preparers...)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare request: %w", err)
	}
	req = req.WithContext(ctx)

	resp, err := c.client.Send(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}

	return resp, nil
}

// HTMLToMarkdown parses basic HTML tags and returns a cleaned Markdown version of the text.
func HTMLToMarkdown(html string) string {
	// Strip head, style, script sections completely
	reScript := regexp.MustCompile(`(?i)<script[\s\S]*?>[\s\S]*?<\/script>`)
	html = reScript.ReplaceAllString(html, "")

	reStyle := regexp.MustCompile(`(?i)<style[\s\S]*?>[\s\S]*?<\/style>`)
	html = reStyle.ReplaceAllString(html, "")

	reHead := regexp.MustCompile(`(?i)<head[\s\S]*?>[\s\S]*?<\/head>`)
	html = reHead.ReplaceAllString(html, "")

	// Headers
	html = regexp.MustCompile(`(?i)<h1[\s\S]*?>(.*?)<\/h1>`).ReplaceAllString(html, "\n# $1\n")
	html = regexp.MustCompile(`(?i)<h2[\s\S]*?>(.*?)<\/h2>`).ReplaceAllString(html, "\n## $1\n")
	html = regexp.MustCompile(`(?i)<h3[\s\S]*?>(.*?)<\/h3>`).ReplaceAllString(html, "\n### $1\n")
	html = regexp.MustCompile(`(?i)<h4[\s\S]*?>(.*?)<\/h4>`).ReplaceAllString(html, "\n#### $1\n")
	html = regexp.MustCompile(`(?i)<h5[\s\S]*?>(.*?)<\/h5>`).ReplaceAllString(html, "\n##### $1\n")
	html = regexp.MustCompile(`(?i)<h6[\s\S]*?>(.*?)<\/h6>`).ReplaceAllString(html, "\n###### $1\n")

	// Bold / Strong
	html = regexp.MustCompile(`(?i)<strong[\s\S]*?>(.*?)<\/strong>`).ReplaceAllString(html, "**$1**")
	html = regexp.MustCompile(`(?i)<b[\s\S]*?>(.*?)<\/b>`).ReplaceAllString(html, "**$1**")

	// Italic / Emphasis
	html = regexp.MustCompile(`(?i)<em[\s\S]*?>(.*?)<\/em>`).ReplaceAllString(html, "*$1*")
	html = regexp.MustCompile(`(?i)<i[\s\S]*?>(.*?)<\/i>`).ReplaceAllString(html, "*$1*")

	// Links: <a href="url">text</a> -> [text](url)
	html = regexp.MustCompile(`(?i)<a\s+[^>]*href=["']([^"']*)["'][^>]*>(.*?)<\/a>`).ReplaceAllString(html, "[$2]($1)")

	// Lists
	html = regexp.MustCompile(`(?i)<ul>\s*<li`).ReplaceAllString(html, "<ul><li")
	html = regexp.MustCompile(`(?i)</li>\s*<li`).ReplaceAllString(html, "</li><li")
	html = regexp.MustCompile(`(?i)</li>\s*</ul>`).ReplaceAllString(html, "</li></ul>")
	html = regexp.MustCompile(`(?i)<li[\s\S]*?>(.*?)<\/li>`).ReplaceAllString(html, "\n- $1")

	// Paragraphs & Breaks
	html = regexp.MustCompile(`(?i)<p[\s\S]*?>(.*?)<\/p>`).ReplaceAllString(html, "\n$1\n")
	html = regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(html, "\n")

	// Strip remaining HTML tags
	reTags := regexp.MustCompile(`<[^>]*>`)
	html = reTags.ReplaceAllString(html, "")

	// Clean up lines containing only whitespace
	html = regexp.MustCompile(`(?m)^\s+$`).ReplaceAllString(html, "")

	// Clean up multi-newlines
	html = regexp.MustCompile(`\n{3,}`).ReplaceAllString(html, "\n\n")

	return strings.TrimSpace(html)
}

// IsFileResponse checks if the response contains binary/file data rather than typical structured text.
func IsFileResponse(resp *http.Response) bool {
	if resp == nil {
		return false
	}
	disposition := resp.Header.Get("Content-Disposition")
	if strings.Contains(strings.ToLower(disposition), "attachment") {
		return true
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if contentType == "" {
		return false
	}

	if strings.Contains(contentType, "application/json") ||
		strings.Contains(contentType, "application/xml") ||
		strings.Contains(contentType, "application/javascript") ||
		strings.Contains(contentType, "text/") {
		return false
	}

	return true
}

// DecodeToMarkdown parses response body content and converts it to Markdown formatting.
// It handles JSON formatting and strips/converts HTML tags.
func DecodeToMarkdown(resp *http.Response) (string, error) {
	if resp == nil || resp.Body == nil {
		return "", fmt.Errorf("response or response body is nil")
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/json") {
		var prettyJSON bytes.Buffer
		if err := json.Indent(&prettyJSON, bodyBytes, "", "  "); err == nil {
			return "```json\n" + prettyJSON.String() + "\n```", nil
		}
		return "```json\n" + string(bodyBytes) + "\n```", nil
	}

	bodyStr := string(bodyBytes)
	if strings.Contains(contentType, "text/html") {
		return HTMLToMarkdown(bodyStr), nil
	}

	return bodyStr, nil
}

// DownloadFile starts downloading a file response asynchronously.
// It returns a DownloadTask immediately which can be used to monitor or cancel the download.
func (c *RESTClient) DownloadFile(ctx context.Context, resp *http.Response, destDir string) (*DownloadTask, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("response or response body is nil")
	}

	downloadCtx, cancel := context.WithCancel(ctx)

	fileName := "downloaded_file"
	disposition := resp.Header.Get("Content-Disposition")
	if disposition != "" {
		if _, params, err := mime.ParseMediaType(disposition); err == nil {
			if fn, ok := params["filename"]; ok && fn != "" {
				fileName = fn
			}
		}
	} else {
		if resp.Request != nil && resp.Request.URL != nil {
			segments := strings.Split(resp.Request.URL.Path, "/")
			if len(segments) > 0 {
				last := segments[len(segments)-1]
				if last != "" {
					fileName = last
				}
			}
		}
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		cancel()
		resp.Body.Close()
		return nil, fmt.Errorf("failed to create destination directory: %w", err)
	}

	filePath := filepath.Join(destDir, fileName)

	out, err := os.Create(filePath)
	if err != nil {
		cancel()
		resp.Body.Close()
		return nil, fmt.Errorf("failed to create output file: %w", err)
	}

	resultChan := make(chan DownloadResult, 1)

	go func() {
		defer resp.Body.Close()

		var downloaded int64
		buffer := make([]byte, 32*1024)
		var err error

		for {
			if ctxErr := downloadCtx.Err(); ctxErr != nil {
				err = ctxErr
				break
			}

			nr, readErr := resp.Body.Read(buffer)
			if nr > 0 {
				nw, writeErr := out.Write(buffer[0:nr])
				if writeErr != nil {
					err = writeErr
					break
				}
				downloaded += int64(nw)
			}
			if readErr != nil {
				if readErr != io.EOF {
					err = readErr
				}
				break
			}
		}

		out.Close() // Close file first so Windows allows deletion

		if err != nil {
			os.Remove(filePath)
			resultChan <- DownloadResult{Error: err}
			return
		}

		resultChan <- DownloadResult{
			FileInfo: FileInfo{
				FileName:    fileName,
				FilePath:    filePath,
				Size:        downloaded,
				ContentType: resp.Header.Get("Content-Type"),
			},
		}
	}()

	return &DownloadTask{
		cancelFunc: cancel,
		ResultChan: resultChan,
	}, nil
}
