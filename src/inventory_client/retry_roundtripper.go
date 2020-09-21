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
	Proxied  http.RoundTripper
	log      *logrus.Logger
	delay    time.Duration
	maxDelay time.Duration
	maxTries uint
}

func (rrt RetryRoundTripper) RoundTrip(req *http.Request) (res *http.Response, e error) {
	b := &backoff.Backoff{
		//These are the defaults
		Min:    rrt.delay,
		Max:    rrt.maxDelay,
		Factor: 2,
		Jitter: false,
	}
	return rrt.retry(rrt.maxTries, b, rrt.Proxied.RoundTrip, req)

}

func (rrt RetryRoundTripper) retry(maxTries uint, backoff *backoff.Backoff, fn func(req *http.Request) (res *http.Response, e error), req *http.Request) (res *http.Response, err error) {
	var i uint
	for i = 1; i <= maxTries; i++ {
		res, err = fn(req)
		if err != nil {
			if i <= maxTries {
				delay := backoff.Duration()
				rrt.log.WithError(err).Warnf("Failed executing HTTP call: %s %s, attempt number %d, Going to retry in: %s, request sent with: HTTP_PROXY: %s, http_proxy: %s, HTTPS_PROXY: %s, https_proxy: %s, NO_PROXY: %s, no_proxy: %s",
					req.Method, req.URL, i, delay, os.Getenv("HTTP_PROXY"), os.Getenv("http_proxy"), os.Getenv("HTTPS_PROXY"), os.Getenv("https_proxy"), os.Getenv("NO_PROXY"), os.Getenv("no_proxy"))
				time.Sleep(delay)
			}
		} else {
			break
		}
	}
	return res, err
}
