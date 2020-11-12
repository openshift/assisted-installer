package ops_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestOps(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ops_test")
}
