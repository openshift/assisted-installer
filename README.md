# OpenShift Assisted Installer
The OpenShift Assisted Installer provides for easy provisioning of new bare metal machines and creation of OpenShift clusters.
The Assisted Installer is meant to run on FCOS or RHCOS liveCD images.
The Asssited Installer uses CoreOS Ignition as a configuration format. The ignition files are created and stored by [Assisted Installation Service](https://github.com/filanov/bm-inventory).

The Assisted Installer is executed by the Assisted Installation Service. Once the cluster installation begins (after host discovery), each host will get the relevant install command from the service. For example:
```bash
sudo podman run -e CLUSTER_ID=<clusterID> -e BUCKET=<S3 bucket> -e S3_URL=<S3 url> -e DEVICE=<boot disk> -v /dev:/dev:rw --privileged --pid=host  quay.io/eranco74/assisted-installer:latest -r <node role>
```

There are 3 different roles for installing a node using the assisted installer: 
 - master
 - worker
 - bootstrap

Unlike most OpenShift installers, the assisted installer requires a minimum of 3 nodes and doesn't require an auxiliary bootstrap node. \
Instead, the Assisted Installer will pivot the bootstrap to become a master node once the control plane is running on 2 other master nodes.

# Bootstrap node installation flow:
The Assisted Installer will:
1. fetch the bootstrap ignition file (currently from S3 but will change soon) and utilize the MCO container for writing the configuration to disk (using once-from option).
1. start the bootstrap services (bootkube.service, approve-csr.service, progress.service), at this point the bootstrap will start a temporary control plane.
1. fetch the cluster kubeconfig from the bm-inventory and wait for 2 master nodes to appear.
1. patch the etcd configuration to allow etcd to start with less than 3 members.
1. wait for 2 **ready** master nodes and for the bootkube service to complete.
1. pivot to master by executing the master installation flow.

# Master / worker node installation flow:
The Assisted Installer will:
1. fetch the relevant ignition file and utilize coreos-installer to write the relevant CoreOS image and ignition to disk.
1. trigger node reboot.

The node will start with the new CoreOS image and ignition, and will contact the machine-config-server running on the bootstrap node in order to complete the installation.

# Known changes to be done:
 - Patch etcd back to it's original configuration, need to check if patch is required for OCP 4.5.0.
 - Create a machine CR for the bootstrap node in order to approve the CSR for this node.
 - Optimize install time by storing the CoreOS image on the live CD rather than downloading it from the internet.
 - Use the relevant CoreOS image for the OCP release.
 - Get the ignition from the bm-inventroy instead of S3.

# Build
To build and push your image to docker registry  just run make.
You can change the default target, export `INSTALLER` environment variable to your docker registry

```bash
export INSTALLER=<registry>/<image-name>:<tag>
make
```
