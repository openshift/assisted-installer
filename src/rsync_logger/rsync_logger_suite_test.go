package rsync_logger

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestRsyncLogger(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "rsync_logger")
}
