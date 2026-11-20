package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

type xrpcError struct {
	Status  int
	Code    string
	Message string
}

func (e *xrpcError) Error() string { return e.Code + ": " + e.Message }

// doJSON makes an HTTP request with an optional JSON body and decodes a JSON
// response into out (if non-nil).
func doJSON(method, endpoint string, params url.Values, body any, token string, out any) error {
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, endpoint, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "thumb/"+version)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("cannot reach %s: %v", req.URL.Host, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}

	if resp.StatusCode >= 400 {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = strconv.Itoa(resp.StatusCode)
		}
		if e.Message == "" {
			e.Message = http.StatusText(resp.StatusCode)
		}
		return &xrpcError{Status: resp.StatusCode, Code: e.Error, Message: e.Message}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}
