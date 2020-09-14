package inventory_client

import (
	"net/http"
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
			rrt.log.Warnf("Failed executing HTTP call: %s %s attempt number %d. Error: %s", req.Method, req.URL, i, err)
			if i <= maxTries {
				delay := backoff.Duration()
				rrt.log.Warnf("Going to retry in: %s", delay.String())
				time.Sleep(delay)
			}
		} else {
			break
		}
	}
	return res, err
}
