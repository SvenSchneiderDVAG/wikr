package main

import (
	"context"
	"io"
	"net/http"
	"time"
)

func httpGet(endpoint string) ([]byte, int, string, error) {
	var lastErr error
	backoff := 150 * time.Millisecond
	const maxAttempts = 3

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), httpTimeout)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			cancel()
			return nil, 0, "", wrapLocalizedError(msgErrHTTPCreateRequest, err)
		}

		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "text/html,application/json;q=0.9,*/*;q=0.8")
		resp, err := httpClient.Do(req)
		if err != nil {
			cancel()
			lastErr = wrapLocalizedError(msgErrHTTPPerformRequest, err)
		} else {
			ct := resp.Header.Get("Content-Type")
			body, rerr := io.ReadAll(resp.Body)
			resp.Body.Close()
			cancel()
			if rerr != nil {
				return nil, resp.StatusCode, ct, wrapLocalizedError(msgErrHTTPReadBody, rerr)
			}
			if resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode >= 500 && resp.StatusCode <= 503) {
				lastErr = newLocalizedError(msgErrHTTPTransientStatus, resp.StatusCode)
			} else {
				return body, resp.StatusCode, ct, nil
			}
		}

		if attempt < maxAttempts {
			time.Sleep(backoff)
			backoff *= 2
		}
	}

	return nil, 0, "", lastErr
}
