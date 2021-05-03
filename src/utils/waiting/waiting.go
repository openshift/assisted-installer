package waiting

import (
	"context"
	"time"

	"github.com/pkg/errors"
)

var (
	GeneralWaitTimeoutInt int           = 30
	GeneralWaitInterval   time.Duration = 30 * time.Second
	WaitTimeout           time.Duration = 70 * time.Minute
)

type wait struct {
	timeout   time.Duration
	interval  time.Duration
	cancelctx context.Context
	message   string
	predicate func() bool
}

func Wait() *wait {
	return &wait{
		timeout:  WaitTimeout,
		interval: GeneralWaitInterval,
	}
}

func (p *wait) WithPredicate(predicate func() bool) *wait {
	p.predicate = predicate
	return p
}

func (p *wait) WithTimeout(timeout time.Duration) *wait {
	p.timeout = timeout
	return p
}

func (p *wait) WithNoTimeout() *wait {
	p.timeout = time.Duration(1<<63 - 1)
	return p
}

func (p *wait) WithInterval(interval time.Duration) *wait {
	p.interval = interval
	return p
}

func (p *wait) WithCancel(cancelctx context.Context) *wait {
	p.cancelctx = cancelctx
	return p
}

func (p *wait) WithMessage(message string) *wait {
	p.message = message
	return p
}

func (p *wait) Start() (error, error, bool) {
	timeoutAfter := time.After(p.timeout)
	ticker := time.NewTicker(p.interval)
	// set default cancel channel here so to not leak context if
	// the cancel is not set from outside
	var cancelctx context.Context
	if cancelctx = p.cancelctx; cancelctx == nil {
		cancelctx = context.TODO()
	}
	// Keep trying until we're time out or get true
	for {
		select {
		// Got a timeout! fail with a timeout error
		case <-timeoutAfter:
			return errors.Errorf("%s timed out", p.message), nil, false
		// Got a cancel signal
		case <-cancelctx.Done():
			return nil, errors.Errorf("%s canceled", p.message), false
		// chech the predicate condition in intervals
		case <-ticker.C:
			if p.predicate() {
				return nil, nil, true
			}
		}
	}
}
