package ops

import (
	"fmt"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/openshift/assisted-installer/src/config"
)

var _ = Describe("ExecCommandError", func() {
	pullSecret := "TEST-TOKEN"
	config.GlobalConfig.PullSecretToken = pullSecret

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
			Args:       []string{"-t", "1", "-m", "-i", "--", "podman", "run", "--net", "host", "--volume", "/:/rootfs:rw", "--volume", "/usr/bin/rpm-ostree:/usr/bin/rpm-ostree", "--privileged", "--entrypoint", "/usr/bin/machine-config-daemon", "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221", "start", "--node-name", "localhost", "--root-mount", "/rootfs", "--once-from", "/opt/install-dir/bootstrap.ign", "--skip-reboot", "--pull-secret", pullSecret},
			Env:        []string{"HOME=/home/userZ", fmt.Sprintf("PULL_SECRET_TOKEN=%s", pullSecret)},
			ExitErr:    fmt.Errorf("exit status 255"),
			WaitStatus: 255,
			Output:     "Trying to pull quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221...\nGetting image source signatures\nCopying blob sha256:74cbb6607642df5f9f70e8588e3c56d6de795d1a9af22866ea4cc82f2dad4f14\nCopying blob sha256:c9fa7d57b9028d4bd02b51cef3c3039fa7b23a8b2d9d26a6ce66b3428f6e2457\nCopying blob sha256:c676df4ac84e718ecee4f8129e43e9c2b7492942606cc65f1fc5e6f3da413160\nCopying blob sha256:b147db91a07555d29ed6085e4733f34dbaa673076488caa8f95f4677f55b3a5c\nCopying blob sha256:ad956945835b7630565fc23fcbd8194eef32b4300c28546d574b2a377fe5d0a5\nCopying config sha256:c4356549f53a30a1baefc5d1515ec1ab8b3786a4bf1738c0abaedc0e44829498\nWriting manifest to image destination\nStoring signatures\nI1019 19:03:28.797092 1 start.go:108] Version: v4.6.0-202008262209.p0-dirty (16d243c4bed178f5d4fd400c0518ebf1dbaface8)\nI1019 19:03:28.797227 1 start.go:118] Calling chroot(\"/rootfs\")\nI1019 19:03:28.797307 1 rpm-ostree.go:261] Running captured: rpm-ostree status --json\nerror: Timeout was reached\nF1019 19:04:35.869592 1 start.go:147] Failed to initialize single run daemon: error reading osImageURL from rpm-ostree: error running rpm-ostree status --json: : exit status 1)",
		}
		wantError := `failed executing nsenter [-t 1 -m -i -- podman run --net host --volume /:/rootfs:rw --volume /usr/bin/rpm-ostree:/usr/bin/rpm-ostree --privileged --entrypoint /usr/bin/machine-config-daemon quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221 start --node-name localhost --root-mount /rootfs --once-from /opt/install-dir/bootstrap.ign --skip-reboot --pull-secret <SECRET>], Error exit status 255, LastOutput "... or: Timeout was reached
F1019 19:04:35.869592 1 start.go:147] Failed to initialize single run daemon: error reading osImageURL from rpm-ostree: error running rpm-ostree status --json: : exit status 1)"`
		wantDetailedError := `failed executing nsenter [-t 1 -m -i -- podman run --net host --volume /:/rootfs:rw --volume /usr/bin/rpm-ostree:/usr/bin/rpm-ostree --privileged --entrypoint /usr/bin/machine-config-daemon quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221 start --node-name localhost --root-mount /rootfs --once-from /opt/install-dir/bootstrap.ign --skip-reboot --pull-secret <SECRET>], env vars [HOME=/home/userZ PULL_SECRET_TOKEN=<SECRET>], error exit status 255, waitStatus 255, Output "Trying to pull quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dc1a34f55c712b2b9c5e5a14dd85e67cbdae11fd147046ac2fef9eaf179ab221...
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

var _ = Describe("installerArgs", func() {
	var (
		device       = "/dev/sda"
		ignitionPath = "/tmp/ignition.ign"
	)

	It("Returns the correct list with no extra args", func() {
		args := installerArgs(ignitionPath, device, nil)
		expected := []string{"install", "--insecure", "-i", "/tmp/ignition.ign", "/dev/sda"}
		Expect(args).To(Equal(expected))
	})

	It("Returns the correct list with empty extra args", func() {
		args := installerArgs(ignitionPath, device, []string{})
		expected := []string{"install", "--insecure", "-i", "/tmp/ignition.ign", "/dev/sda"}
		Expect(args).To(Equal(expected))
	})

	It("Returns the correct list with extra args", func() {
		args := installerArgs(ignitionPath, device, []string{"-n", "--append-karg", "nameserver=8.8.8.8"})
		expected := []string{"install", "--insecure", "-i", "/tmp/ignition.ign", "-n", "--append-karg", "nameserver=8.8.8.8", "/dev/sda"}
		Expect(args).To(Equal(expected))
	})
})
