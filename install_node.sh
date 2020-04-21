#!/usr/local/bin/bash
set -euo pipefail

MACHINE_CONFIG_IMAGE=docker.io/eranco/mcd:latest
INSTALL_DIR=/opt/insall-dir
podman_run() {
  nsenter_run podman run --net=host "${@}"
}

nsenter_run(){
  echo "Running: nsenter -t 1 -m -- $*"
  nsenter -t 1 -m --  "${@}"
}
bootstrap() {
  echo "Starting bootstrap flow"
  nsenter_run mkdir -p $INSTALL_DIR
  echo "Get bootstrap.ign"
  nsenter_run curl -s $S3_URL/$BUCKET/$CLUSTER_ID/bootstrap.ign -o $INSTALL_DIR/bootstrap.ign

  echo "Writing bootstrap ignition to disk"
  podman_run \
    --volume "/:/rootfs:rw" \
    --privileged \
    --entrypoint /machine-config-daemon \
    "${MACHINE_CONFIG_IMAGE}" \
    start --node-name localhost --root-mount /rootfs --once-from $INSTALL_DIR/bootstrap.ign --skip-reboot

  echo "Starting bootkube.service"
  nsenter_run systemctl start bootkube.service
  echo "Starting approve-csr.service"
  nsenter_run systemctl start approve-csr.service
  echo "Starting progress.service"
  nsenter_run systemctl start progress.service

  echo "Done Setting up bootstrap"
}

install_bootstrap() {
  bootstrap
  get_kubeconfig
  wait_for_nodes
  patch_etcd
  install_node master
  wait_for_nodes_ready
  wait_for_bootkube
}

install_node() {
  echo "Create install-dir"
  nsenter_run mkdir -p $INSTALL_DIR
  IGNITION_FILE=${@}".ign"
  echo "Get $IGNITION_FILE"
  nsenter_run curl -s $S3_URL/$BUCKET/$CLUSTER_ID/$IGNITION_FILE -o $INSTALL_DIR/$IGNITION_FILE

  echo "Writing image and ignition to disk"
  nsenter_run sudo coreos-installer install --image-url https://mirror.openshift.com/pub/openshift-v4/dependencies/rhcos/4.4/latest/rhcos-4.4.0-rc.1-x86_64-metal.x86_64.raw.gz --insecure -i $INSTALL_DIR/$IGNITION_FILE $DEVICE
  echo "Done"
}

wait_for_nodes() {
  echo "Waiting for 2 master nodes"
  until [ $(nsenter_run kubectl --kubeconfig=$INSTALL_DIR/auth/kubeconfig get nodes | grep master | wc -l) -eq 2 ]; do
    sleep 5s
  done
  echo "Got 2 master nodes"
  echo -e "$(nsenter_run kubectl --kubeconfig=$INSTALL_DIR/auth/kubeconfig get nodes)"
}

wait_for_nodes_ready() {
  echo "Waiting for 2 ready master nodes"
  until [ $(nsenter_run kubectl --kubeconfig=$INSTALL_DIR/auth/kubeconfig get nodes | grep master | grep -v NotReady | grep Ready | wc -l) -eq 2 ]; do
    sleep 5s
  done
  echo "Got 2 ready master nodes"
  echo -e "$(nsenter_run kubectl --kubeconfig=$INSTALL_DIR/auth/kubeconfig get nodes)"
}

wait_for_bootkube() {
  echo "Waiting for bootkube to finish"
  until [ $(nsenter_run systemctl status bootkube.service | grep "bootkube.service: Succeeded" | wc -l) -eq 1 ]; do
    sleep 5s
  done
  echo "bootkube service completed"
  echo -e "$(nsenter_run systemctl status bootkube.service)"
}

reboot_node() {
  echo "Rebooting node"
  nsenter_run sudo reboot
}

patch_etcd() {
  echo "Patching etcd to allow less than 3 members"
  nsenter_run oc --kubeconfig=$INSTALL_DIR/auth/kubeconfig patch etcd cluster -p='{"spec": {"unsupportedConfigOverrides": {"useUnsupportedUnsafeNonHANonProductionUnstableEtcd": true}}}' --type=merge
}

get_kubeconfig() {
  nsenter_run mkdir -p $INSTALL_DIR/auth

  echo "Get kubeconfig"
  nsenter_run curl -s $S3_URL/$BUCKET/$CLUSTER_ID/kubeconfig -o $INSTALL_DIR/auth/kubeconfig
}

valid_roles=("bootstrap" "master" "worker")

help_func() {
  echo ""
  echo "Usage: $0 -r <node role>"
  echo -e "\t-r ${valid_roles[@]/%/\|}"
  exit 1 # Exit script after printing help
}

invalid_role() {
  echo "$NODE_ROLE: not recognized. Valid roles are:"
  echo "${valid_roles[@]/%/,}"
  exit 1
}

# Print help_func in case parameters are empty
NODE_ROLE="node role"
while getopts "r:" opt; do
  case "$opt" in
  r) NODE_ROLE="$OPTARG" ;;
  ?) help_func ;; # Print help_func in case parameter is non-existent
  esac
done

# Begin script in case all parameters are correct
case "$NODE_ROLE" in
bootstrap) install_bootstrap
           reboot_node ;;
worker) install_node worker
        reboot_node ;;
master) install_node master
        reboot_node ;;
*) invalid_role ;;
esac
