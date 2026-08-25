package execute

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-installer/src/config"
	"github.com/sirupsen/logrus"
)

var _ = Describe("ExecCommandError", func() {
	It("Creates the correct error for mkdir", func() {
		err := &ExecCommandError{
			Command: "mkdir",
			Args:    []string{"-p", "/somedir"},
			Env:     []string{"HOME=/home/userZ"},
			ExitErr: fmt.Errorf("Permission denied"),
			Output:  "mkdir: cannot create directory ‘/somedir’: Permission denied",
		}
		wantError := "failed executing mkdir [-p /somedir], Error Permission denied, LastOutput \"mkdir: cannot create directory ‘/somedir’: Permission denied\""
		wantDetailedError := "failed executing mkdir [-p /somedir], env vars [HOME=/home/userZ], error Permission denied, waitStatus 0, Output \"mkdir: cannot create directory ‘/somedir’: Permission denied\""

		Expect(err.Error()).To(Equal(wantError))
		Expect(err.DetailedError()).To(Equal(wantDetailedError))
	})

	It("Creates the correct error for ignition extract", func() {
		err := &ExecCommandError{
			Command:    "nsenter",
			Args:       []string{"-t", "1", "-m", "-i", "--", "podman", "run", "--net", "host", "--volume", "/:/rootfs:rw", "--volume", "/usr/bin/rpm-ostree:/usr/bin/rpm-ostree", "--privileged", "--entrypoint", "/usr/bin/machine-config-daemon", "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221", "start", "--node-name", "localhost", "--root-mount", "/rootfs", "--once-from", "/opt/install-dir/bootstrap.ign", "--skip-reboot"},
			Env:        []string{"HOME=/home/userZ", "PULL_SECRET_TOKEN=TEST-TOKEN"},
			ExitErr:    fmt.Errorf("exit status 255"),
			WaitStatus: 255,
			Output:     "Trying to pull quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221...\nGetting image source signatures\nCopying blob sha256:74cbb6607642df5f9f70e8588e3c56d6de795d1a9af22866ea4cc82f2dad4f14\nCopying blob sha256:c9fa7d57b9028d4bd02b51cef3c3039fa7b23a8b2d9d26a6ce66b3428f6e2457\nCopying blob sha256:c676df4ac84e718ecee4f8129e43e9c2b7492942606cc65f1fc5e6f3da413160\nCopying blob sha256:b147db91a07555d29ed6085e4733f34dbaa673076488caa8f95f4677f55b3a5c\nCopying blob sha256:ad956945835b7630565fc23fcbd8194eef32b4300c28546d574b2a377fe5d0a5\nCopying config sha256:c4356549f53a30a1baefc5d1515ec1ab8b3786a4bf1738c0abaedc0e44829498\nWriting manifest to image destination\nStoring signatures\nI1019 19:03:28.797092 1 start.go:108] Version: v4.6.0-202008262209.p0-dirty (16d243c4bed178f5d4fd400c0518ebf1dbaface8)\nI1019 19:03:28.797227 1 start.go:118] Calling chroot(\"/rootfs\")\nI1019 19:03:28.797307 1 rpm-ostree.go:261] Running captured: rpm-ostree status --json\nerror: Timeout was reached\nF1019 19:04:35.869592 1 start.go:147] Failed to initialize single run daemon: error reading osImageURL from rpm-ostree: error running rpm-ostree status --json: : exit status 1)",
		}
		wantError := `failed executing nsenter [-t 1 -m -i -- podman run --net host --volume /:/rootfs:rw --volume /usr/bin/rpm-ostree:/usr/bin/rpm-ostree --privileged --entrypoint /usr/bin/machine-config-daemon quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221 start --node-name localhost --root-mount /rootfs --once-from /opt/install-dir/bootstrap.ign --skip-reboot], Error exit status 255, LastOutput "... or: Timeout was reached
F1019 19:04:35.869592 1 start.go:147] Failed to initialize single run daemon: error reading osImageURL from rpm-ostree: error running rpm-ostree status --json: : exit status 1)"`
		wantDetailedError := `failed executing nsenter [-t 1 -m -i -- podman run --net host --volume /:/rootfs:rw --volume /usr/bin/rpm-ostree:/usr/bin/rpm-ostree --privileged --entrypoint /usr/bin/machine-config-daemon quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221 start --node-name localhost --root-mount /rootfs --once-from /opt/install-dir/bootstrap.ign --skip-reboot], env vars [HOME=/home/userZ PULL_SECRET_TOKEN=<REDACTED>], error exit status 255, waitStatus 255, Output "Trying to pull quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221...
Getting image source signatures
Copying blob sha256:74cbb6607642df5f9f70e8588e3c56d6de795d1a9af22866ea4cc82f2dad4f14
Copying blob sha256:c9fa7d57b9028d4bd02b51cef3c3039fa7b23a8b2d9d26a6ce66b3428f6e2457
Copying blob sha256:c676df4ac84e718ecee4f8129e43e9c2b7492942606cc65f1fc5e6f3da413160
Copying blob sha256:b147db91a07555d29ed6085e4733f34dbaa673076488caa8f95f4677f55b3a5c
Copying blob sha256:ad956945835b7630565fc23fcbd8194eef32b4300c28546d574b2a377fe5d0a5
Copying config sha256:c4356549f53a30a1baefc5d1515ec1ab8b3786a4bf1738c0abaedc0e44829498
Writing manifest to image destination
Storing signatures
I1019 19:03:28.797092 1 start.go:108] Version: v4.6.0-202008262209.p0-dirty (16d243c4bed178f5d4fd400c0518ebf1dbaface8)
I1019 19:03:28.797227 1 start.go:118] Calling chroot("/rootfs")
I1019 19:03:28.797307 1 rpm-ostree.go:261] Running captured: rpm-ostree status --json
error: Timeout was reached
F1019 19:04:35.869592 1 start.go:147] Failed to initialize single run daemon: error reading osImageURL from rpm-ostree: error running rpm-ostree status --json: : exit status 1)"`

		Expect(err.Error()).To(Equal(wantError))
		Expect(err.DetailedError()).To(Equal(wantDetailedError))
	})
})

var _ = Describe("ExecCommandWithOptions", func() {
	It("should combine base environment with additional env from WithEnv", func() {
		executor := NewExecutor(&config.Config{}, logrus.New(), false).(*executor)

		additionalEnv := []string{"TEST_VAR=test_value", "PULL_SECRET_TOKEN=secret"}
		cfg := &commandConfig{command: "sh", args: []string{"-c", "env"}}

		WithEnv(additionalEnv)(cfg)
		Expect(cfg.env).To(Equal(additionalEnv), "WithEnv should append additional env")

		// Simulate what execCommand does - prepend base env
		finalEnv := append(append([]string(nil), executor.cmdEnv...), cfg.env...)

		Expect(finalEnv).To(ContainElement("TEST_VAR=test_value"),
			"Final env should contain additional vars")
		Expect(finalEnv).To(ContainElement("PULL_SECRET_TOKEN=secret"),
			"Final env should contain secret token")
		Expect(len(finalEnv)).To(Equal(len(executor.cmdEnv)+len(additionalEnv)),
			"Final env should have base + additional vars")
	})

	It("should support multiple WithEnv calls", func() {
		cfg := &commandConfig{command: "sh", args: []string{"-c", "env"}}

		WithEnv([]string{"VAR1=value1"})(cfg)
		WithEnv([]string{"VAR2=value2"})(cfg)

		Expect(cfg.env).To(Equal([]string{"VAR1=value1", "VAR2=value2"}),
			"Multiple WithEnv calls should compose")
	})

	It("should redact sensitive environment variables from logs", func() {
		testEnv := []string{
			"PULL_SECRET_TOKEN=secret123",
			"API_KEY=apikey456",
			"DATABASE_PASSWORD=dbpass789",
			"HOME=/home/user",
			"PATH=/usr/bin",
		}

		redacted := redactSensitiveEnv(testEnv)

		Expect(redacted).To(ContainElement("PULL_SECRET_TOKEN=<REDACTED>"),
			"TOKEN should be redacted")
		Expect(redacted).To(ContainElement("API_KEY=<REDACTED>"),
			"KEY should be redacted")
		Expect(redacted).To(ContainElement("DATABASE_PASSWORD=<REDACTED>"),
			"PASSWORD should be redacted")
		Expect(redacted).To(ContainElement("HOME=/home/user"),
			"Non-sensitive vars should not be redacted")
		Expect(redacted).To(ContainElement("PATH=/usr/bin"),
			"Non-sensitive vars should not be redacted")

		Expect(redacted).ToNot(ContainElement(ContainSubstring("secret123")),
			"Secret values should not appear in redacted output")
		Expect(redacted).ToNot(ContainElement(ContainSubstring("apikey456")),
			"Secret values should not appear in redacted output")
		Expect(redacted).ToNot(ContainElement(ContainSubstring("dbpass789")),
			"Secret values should not appear in redacted output")
	})

	It("should wrap command in nsenter with WithPrivilege", func() {
		cfg := &commandConfig{command: "podman", args: []string{"run", "--rm", "image"}}

		WithPrivilege()(cfg)

		Expect(cfg.command).To(Equal("nsenter"))
		Expect(cfg.args).To(Equal([]string{
			"--target", "1",
			"--cgroup", "--mount", "--ipc", "--pid",
			"--",
			"podman", "run", "--rm", "image",
		}))
	})

	It("should compose WithPrivilege and WithEnv", func() {
		cfg := &commandConfig{command: "podman", args: []string{"run", "image"}}

		WithPrivilege()(cfg)
		WithEnv([]string{"TOKEN=secret"})(cfg)

		Expect(cfg.command).To(Equal("nsenter"))
		Expect(cfg.env).To(Equal([]string{"TOKEN=secret"}))
	})
})
