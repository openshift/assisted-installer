package config

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-service/models"
)

func TestConfig(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "config_test")
}

var _ = Describe("ProcessArgs", func() {

	It("Should not deserialize installer args when they are not supplied.", func() {
		config := &Config{}
		arguments := []string{"--role", "worker", "--cluster-id", "0ae63135-5f7c-431e-9c72-0efaf2cb83b8"}
		config.ProcessArgs(arguments)
		Expect(len(config.InstallerArgs)).To(BeZero())
	})

	It("Should deserialize installer args correctly when they are supplied correctly.", func() {
		config := &Config{}
		arguments := []string{"--role", "worker", "--cluster-id", "0ae63135-5f7c-431e-9c72-0efaf2cb83b8", "--installer-args", "[\"arg1=foo\", \"arg2=bar\"]"}
		config.ProcessArgs(arguments)
		Expect(len(config.InstallerArgs)).To(Equal(2))
		Expect(config.InstallerArgs[0]).To(Equal("arg1=foo"))
		Expect(config.InstallerArgs[1]).To(Equal("arg2=bar"))
	})

	It("HighAvailabilityMode should be set to empty string if the host role is worker.", func() {
		config := &Config{}
		arguments := []string{"--role", string(models.HostRoleWorker), "--cluster-id", "0ae63135-5f7c-431e-9c72-0efaf2cb83b8", "--high-availability-mode", models.ClusterHighAvailabilityModeFull}
		config.ProcessArgs(arguments)
		Expect(config.HighAvailabilityMode).To(BeEmpty())
	})

	It("HighAvailabilityMode should be unchanged if the host role is master.", func() {
		config := &Config{}
		arguments := []string{"--role", string(models.HostRoleMaster), "--cluster-id", "0ae63135-5f7c-431e-9c72-0efaf2cb83b8", "--high-availability-mode", models.ClusterHighAvailabilityModeFull}
		config.ProcessArgs(arguments)
		Expect(config.HighAvailabilityMode).To(Equal(models.ClusterHighAvailabilityModeFull))
	})

	It("HighAvailabilityMode should be unchanged if the host role is bootstrap.", func() {
		config := &Config{}
		arguments := []string{"--role", string(models.HostRoleBootstrap), "--cluster-id", "0ae63135-5f7c-431e-9c72-0efaf2cb83b8", "--high-availability-mode", models.ClusterHighAvailabilityModeFull}
		config.ProcessArgs(arguments)
		Expect(config.HighAvailabilityMode).To(Equal(models.ClusterHighAvailabilityModeFull))
	})

	It("InfraEnvId should be set to ClusterId if the InfraEnvId is not defined", func() {
		config := &Config{}
		arguments := []string{"--role", string(models.HostRoleBootstrap), "--cluster-id", "0ae63135-5f7c-431e-9c72-0efaf2cb83b8", "--high-availability-mode", models.ClusterHighAvailabilityModeFull}
		config.ProcessArgs(arguments)
		Expect(config.InfraEnvID).To(Equal(config.ClusterID))
	})

	It("InfraEnvId should not be set to ClusterId if the InfraEnvId is defined", func() {
		config := &Config{}
		arguments := []string{"--role", string(models.HostRoleBootstrap), "--infra-env-id", "9f2a26d7-10a6-4be0-b1c2-e895ad3b04b8", "--cluster-id", "0ae63135-5f7c-431e-9c72-0efaf2cb83b8", "--high-availability-mode", models.ClusterHighAvailabilityModeFull}
		config.ProcessArgs(arguments)
		Expect(config.InfraEnvID).To(Equal("9f2a26d7-10a6-4be0-b1c2-e895ad3b04b8"))
	})

})

var _ = Describe("SetInstallerArgs", func() {

	It("Should raise an error when supplied installer args could not be parsed.", func() {
		config := &Config{}
		Expect(config.SetInstallerArgs("Non JSON string!!!")).Error()
		Expect(len(config.InstallerArgs)).To(BeZero())
	})

})
