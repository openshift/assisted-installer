# Openshift assisted-installer

The Openshift assisted installer handles provisioning of new machines and execute the installation logic on these machines to create an openshift cluster. \
The aim of assisted insaller is to reduce requirements and allow easy Day 1 bare metal installation.  
The assisted installer is meant to run on FCOS or RHCOS liveCD images.
The asssited installer uses CoreOS Ignition as a configuration format, the ignition files are created and stored by [bm-inventory](https://github.com/filanov/bm-inventory).

The assisted installer is getting executed by the bm-inventory, upon cluster install each node will get the relevant install command e.g.
```shell script
sudo podman run -e CLUSTER_ID=<clusterID> -e BUCKET=<>> -e S3_URL=<S3 url> -e DEVICE=<boot disk> -v /dev:/dev:rw --privileged --pid=host  quay.io/eranco74/assisted-installer:latest -r <node role>
```

There are 3 different roles for installing a node using the assisted installer: 
 - master
 - worker
 - bootstrap

Unlike must openshift installers the assisted installer require minimum of 3 nodes and doesn't require an auxiliary bootstrap node. \
Instead the assisted installer will pivot the bootstrap to become a master node once we have control plane running on 2 other master nodes.

# bootstrap node installation flow:
The assisted installer will fetch the bootstrap ignition file (currently from S3 but will change soon) and utilize the MCO container for writing the configuration to disk (using once-from option).
The assisted installer will start the bootstrap services (bootkube.service, approve-csr.service, progress.service), at this point the bootstrap will start a temporary control plane. \
The assisted installer will fetch the clluster kubeconfig from the bm-inventory and will wait for 2 master nodes.
The assisted installer will patch etcd configuration to allow etcd to start with less than 3 members.
Due to the time it takes to download and write the coreos image to disk the assisted installer will run the master installation flow without reboot.
Once done the assisted installer will wait for 2 ready master nodes and for the bootkube service to complete.
At this point the assisted installer will reboot the bootstrap node in order for it to start as the 3rd master.

 
# master / worker node installation flow:
The assisted installer will fetch the relevant ignition file and utilize coreos-insatller to write the relevant coreos image and ignition to disk.
Once done the assisted installer will trigger node reboot.
The node will start with the new RHCOS image and ignition and will turn to the machine-config-server running on the bootstrap node in order to complete the installation.

# Required changes to the flow: 
 - Patch etcd back to it's original configuration, need to check if patch is required for OCP 4.5.0.
 - Create a machine CR for the bootstrap node it order to approve the CSR for this node.
 - Don't download the coreos image from the internet (perhaps it should be in the liveCD.
 - Use The relevant RHCOS image for the OCP release.
 - Get the ignition from the bm-inventroy instead of S3.


## Build

To build and push your image to docker registry  just run make. \
You can change the default target, export `INSTALLER` environment variable to your docker registry

```shell script
export INSTALLER=<registry>/<image-name>:<tag>
make
```
