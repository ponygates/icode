package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type HTTPClient struct {
	client    *http.Client
	baseURL   string
	apiKey    string
	headers   map[string]string
	tokenGen  func() string
}

func NewHTTPClient(baseURL, apiKey string, opts ...func(*HTTPClient)) *HTTPClient {
	c := &HTTPClient{
		client: &http.Client{
			Timeout: 180 * time.Second,
		},
		baseURL: baseURL,
		apiKey:  apiKey,
		headers: map[string]string{
			"Content-Type": "application/json",
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func WithHeader(k, v string) func(*HTTPClient) {
	return func(c *HTTPClient) {
		c.headers[k] = v
	}
}

func WithTokenGen(fn func() string) func(*HTTPClient) {
	return func(c *HTTPClient) {
		c.tokenGen = fn
	}
}

func (c *HTTPClient) PostJSON(path string, body, resp any) error {
	url := c.baseURL + path

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	c.setAuth(req)

	res, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("http post: %w", err)
	}
	defer res.Body.Close()

	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		return fmt.Errorf("api error (HTTP %d): %s", res.StatusCode, string(raw))
	}

	if resp != nil {
		if err := json.Unmarshal(raw, resp); err != nil {
			return fmt.Errorf("decode response: %w\nbody: %s", err, string(raw))
		}
	}

	return nil
}

func (c *HTTPClient) PostSSE(path string, body any) (<-chan string, error) {
	url := c.baseURL + path

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequest("POST", url, &buf)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	c.setAuth(req)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	res, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http post stream: %w", err)
	}

	if res.StatusCode >= 400 {
		raw, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return nil, fmt.Errorf("api error (HTTP %d): %s", res.StatusCode, string(raw))
	}

	ch := make(chan string, 64)

	go func() {
		defer res.Body.Close()
		defer close(ch)

		scanner := NewSSEScanner(res.Body)
		for {
			line, err := scanner.ReadLine()
			if err != nil {
				return
			}

			data := extractSSEData(line)
			if data == nil {
				continue
			}

			dataStr := string(data)
			if dataStr == "[DONE]" {
				return
			}

			ch <- dataStr
		}
	}()

	return ch, nil
}

func (c *HTTPClient) setAuth(req *http.Request) {
	if c.tokenGen != nil {
		req.Header.Set("Authorization", "Bearer "+c.tokenGen())
	} else if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
}

func extractSSEData(line []byte) []byte {
	if !bytes.HasPrefix(line, []byte("data: ")) {
		return nil
	}
	return bytes.TrimPrefix(line, []byte("data: "))
}

type SSEScanner struct {
	reader io.Reader
	buf    []byte
}

func NewSSEScanner(r io.Reader) *SSEScanner {
	return &SSEScanner{
		reader: r,
		buf:    make([]byte, 0, 4096),
	}
}

func (s *SSEScanner) ReadLine() ([]byte, error) {
	for {
		idx := bytes.IndexByte(s.buf, '\n')
		if idx >= 0 {
			line := s.buf[:idx]
			s.buf = s.buf[idx+1:]
			return line, nil
		}

		tmp := make([]byte, 1024)
		n, err := s.reader.Read(tmp)
		if err != nil {
			if len(s.buf) > 0 {
				line := s.buf
				s.buf = s.buf[:0]
				return line, nil
			}
			return nil, err
		}

		s.buf = append(s.buf, tmp[:n]...)
	}
}
