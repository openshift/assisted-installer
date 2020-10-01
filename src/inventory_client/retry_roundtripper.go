package inventory_client

import (
	"net/http"
	"os"
	"time"

	"github.com/jpillora/backoff"
	"github.com/sirupsen/logrus"
)

// This type implements the http.RoundTripper interface
type RetryRoundTripper struct {
	Proxied    http.RoundTripper
	log        *logrus.Logger
	delay      time.Duration
	maxDelay   time.Duration
	maxRetries uint
}

func (rrt RetryRoundTripper) RoundTrip(req *http.Request) (res *http.Response, e error) {
	b := &backoff.Backoff{
		//These are the defaults
		Min:    rrt.delay,
		Max:    rrt.maxDelay,
		Factor: 2,
		Jitter: false,
	}
	return rrt.retry(rrt.maxRetries, b, rrt.Proxied.RoundTrip, req)

}

func (rrt RetryRoundTripper) retry(maxRetries uint, backoff *backoff.Backoff, fn func(req *http.Request) (res *http.Response, e error), req *http.Request) (res *http.Response, err error) {
	var i uint
	for i = 1; i <= maxRetries; i++ {
		res, err = fn(req)
		if err != nil || (res != nil && (res.StatusCode < 200 || res.StatusCode >= 300)) {
			if i <= maxRetries {
				delay := backoff.Duration()

				fields := logrus.Fields{
					"method":      req.Method,
					"url":         req.URL,
					"attempt":     i,
					"delay":       delay,
					"HTTP_PROXY":  os.Getenv("HTTP_PROXY"),
					"HTTPS_PROXY": os.Getenv("HTTPS_PROXY"),
					"NO_PROXY":    os.Getenv("NO_PROXY"),
				}

				if res != nil {
					fields["statusCode"] = res.StatusCode
				}

				rrt.log.WithFields(fields).WithError(err).Warn("Failed executing HTTP call")
				time.Sleep(delay)
			}
		} else {
			break
		}
	}
	return res, err
}
