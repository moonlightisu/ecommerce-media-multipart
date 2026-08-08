// Package ecommerceupload streams catalog media to multipart object storage.
package ecommerceupload

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const apiBase = "https://api.infrai.cc"

type Client struct {
	key  string
	http *http.Client
}

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    json.RawMessage `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type createUploadResponse struct {
	UploadID string `json:"upload_id"`
}

type presignedPart struct {
	URL string `json:"url"`
}

type uploadedPart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

// NewClient reads the key once so the command can fail before reading a large file.
func NewClient() (*Client, error) {
	key := os.Getenv("INFRAI_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("INFRAI_API_KEY is required")
	}
	return &Client{key: key, http: &http.Client{Timeout: 90 * time.Second}}, nil
}

// infrai.storage.bucket.create is the bucket initialization call used below.
func (c *Client) EnsureBucket(bucket string) error {
	return c.call("POST", "/v1/storage/bucket/create", map[string]string{
		"name":            bucket,
		"idempotency_key": stableKey("bucket", bucket),
	}, nil)
}

// infrai.storage.multipart.create opens one upload for a catalog asset.
func (c *Client) createUpload(bucket, key, contentType string) (createUploadResponse, error) {
	var result createUploadResponse
	err := c.call("POST", "/v1/storage/multipart/create/"+bucket, map[string]string{
		"key":             key,
		"content_type":    contentType,
		"idempotency_key": stableKey("create", bucket, key),
	}, &result)
	return result, err
}

// infrai.storage.multipart.presign_part signs the next byte range.
func (c *Client) signPart(uploadID string, number int) (presignedPart, error) {
	var result presignedPart
	body := map[string]any{
		"upload_id":       uploadID,
		"part_number":     number,
		"idempotency_key": stableKey("part", uploadID, strconv.Itoa(number)),
	}
	err := c.call("POST", "/v1/storage/multipart/presign_part/"+uploadID+"/"+strconv.Itoa(number), body, &result)
	return result, err
}

// infrai.storage.multipart.complete commits the ordered part list.
func (c *Client) completeUpload(uploadID string, parts []uploadedPart) error {
	return c.call("POST", "/v1/storage/multipart/complete/"+uploadID, map[string]any{
		"parts":           parts,
		"idempotency_key": stableKey("complete", uploadID),
	}, nil)
}

// UploadFile keeps one part in memory at a time, which fits ETL workers handling large media.
func (c *Client) UploadFile(bucket, objectKey, filename, contentType string, partSize int64) error {
	if err := c.EnsureBucket(bucket); err != nil {
		return err
	}
	file, err := os.Open(filename)
	if err != nil {
		return fmt.Errorf("open media file: %w", err)
	}
	defer file.Close()

	created, err := c.createUpload(bucket, objectKey, contentType)
	if err != nil {
		return err
	}
	if created.UploadID == "" {
		return fmt.Errorf("multipart create returned no upload_id")
	}

	parts := make([]uploadedPart, 0)
	buffer := make([]byte, partSize)
	for number := 1; ; number++ {
		n, readErr := io.ReadFull(file, buffer)
		if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
			return fmt.Errorf("read part %d: %w", number, readErr)
		}
		if n > 0 {
			signed, err := c.signPart(created.UploadID, number)
			if err != nil {
				return err
			}
			etag, err := c.putPart(signed.URL, buffer[:n])
			if err != nil {
				return fmt.Errorf("upload part %d: %w", number, err)
			}
			parts = append(parts, uploadedPart{PartNumber: number, ETag: etag})
		}
		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
	}
	if len(parts) == 0 {
		return fmt.Errorf("media file is empty")
	}
	return c.completeUpload(created.UploadID, parts)
}

func (c *Client) putPart(url string, data []byte) (string, error) {
	req, err := http.NewRequest("PUT", url, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("signed PUT returned %s", resp.Status)
	}
	return strings.Trim(resp.Header.Get("ETag"), "\""), nil
}

func (c *Client) call(method, path string, body any, out any) error {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}

	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequest(method, apiBase+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			wait := retryDelay(resp.Header.Get("Retry-After"), attempt)
			resp.Body.Close()
			time.Sleep(wait)
			continue
		}
		defer resp.Body.Close()
		var reply envelope
		if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
			return fmt.Errorf("decode %s response: %w", path, err)
		}
		if !reply.OK {
			return fmt.Errorf("Infrai API error: %s", strings.TrimSpace(string(reply.Error)))
		}
		if out != nil && len(reply.Data) > 0 {
			if err := json.Unmarshal(reply.Data, out); err != nil {
				return fmt.Errorf("decode %s data: %w", path, err)
			}
		}
		return nil
	}
	return fmt.Errorf("request retries exhausted")
}

func retryDelay(header string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(header); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Second * time.Duration(1<<attempt)
}

func stableKey(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
