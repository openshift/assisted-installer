package installer

const (
	sshDir                       = "/root/.ssh"
	sshKeyPath                   = sshDir + "/id_rsa"
	sshPubKeyPath                = sshKeyPath + ".pub"
	assistedInstallerSshManifest = "/opt/openshift/openshift/99_openshift-machineconfig_99-assisted-installer-master-ssh.yaml"
	sshManifestTmpl              = `
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: master
  name: 99-assisted-installer-master-ssh
spec:
  config:
    ignition:
      version: 3.1.0
    passwd:
      users:
      - name: core
        sshAuthorizedKeys:
        - {{.SshPubKey}}
`
)
