package prices

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// DefaultTimeout bounds one request to a price source when the caller gives
// no client of its own.
const DefaultTimeout = 30 * time.Second

// maxBody bounds a source's response. A few years of daily bars for one stock
// is well under this.
const maxBody = 4 << 20

func defaultClient(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: DefaultTimeout}
}

// get sends req and returns the status code and body. A transport error is
// returned without its URL, because a source's URL may carry its key.
func get(c *http.Client, req *http.Request, source string) (int, []byte, error) {
	resp, err := c.Do(req) //nolint:gosec // the URL is built from a configured base and a validated symbol
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return 0, nil, ctxErr
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, nil, fmt.Errorf("%s: request failed: %s", source, redactedError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("%s: reading response: %s", source, redactedError(err))
	}
	if len(body) > maxBody {
		return resp.StatusCode, nil, fmt.Errorf("%w: %s: response is larger than %d bytes", ErrBadResponse, source, maxBody)
	}
	return resp.StatusCode, body, nil
}

// redactedError gives an error's text, or a fixed phrase if the text could
// hold a URL.
func redactedError(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	var ue *url.Error
	if errors.As(err, &ue) {
		return "transport error"
	}
	return err.Error()
}
