package execute

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestExecute(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "execute_test")
}
