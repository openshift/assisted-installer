package common

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/openshift/assisted-installer/src/inventory_client"
	"github.com/openshift/assisted-installer/src/k8s_client"
	"github.com/openshift/assisted-installer/src/utils"
	"github.com/openshift/assisted-service/models"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/thoas/go-funk"
	v1 "k8s.io/api/core/v1"
)

const (
	ControllerLogsSecondsAgo       = 60 * 60
	AssistedControllerIsReadyEvent = "AssistedControllerIsReady"
	AssistedControllerPrefix       = "assisted-installer-controller"
)

func GetHostsInStatus(hosts map[string]inventory_client.HostData, status []string, isMatch bool) map[string]inventory_client.HostData {
	hostsbystatus := make(map[string]inventory_client.HostData)
	for hostname, hostData := range hosts {
		if isMatch == funk.ContainsString(status, *hostData.Host.Status) {
			hostsbystatus[hostname] = hostData
		}
	}
	return hostsbystatus
}

func SetConfiguringStatusForHosts(client inventory_client.InventoryClient, inventoryHostsMapWithIp map[string]inventory_client.HostData,
	mcsLogs string, fromBootstrap bool, log logrus.FieldLogger) {
	notValidStates := map[models.HostStage]struct{}{models.HostStageConfiguring: {}, models.HostStageJoined: {}, models.HostStageDone: {}}
	if fromBootstrap {
		notValidStates[models.HostStageWaitingForIgnition] = struct{}{}
	}
	for hostName, host := range inventoryHostsMapWithIp {
		_, ok := notValidStates[host.Host.Progress.CurrentStage]
		if ok {
			continue
		}
		log.Infof("Verifying if host %s pulled ignition", hostName)
		pat := fmt.Sprintf("(%s).{1,40}(Ignition)", strings.Join(host.IPs, "|"))
		pattern, err := regexp.Compile(pat)
		if err != nil {
			log.WithError(err).Errorf("Failed to compile regex from host %s ips list", hostName)
			return
		}
		if pattern.MatchString(mcsLogs) {
			status := models.HostStageConfiguring
			if fromBootstrap && host.Host.Role == models.HostRoleWorker {
				status = models.HostStageWaitingForIgnition
			}
			ctx := utils.GenerateRequestContext()
			requestLog := utils.RequestIDLogger(ctx, log)
			requestLog.Infof("Host %s %q found in mcs logs, moving it to %s state", hostName, host.Host.ID.String(), status)
			if err := client.UpdateHostInstallProgress(ctx, host.Host.InfraEnvID.String(), host.Host.ID.String(), status, ""); err != nil {
				requestLog.Errorf("Failed to update node installation status, %s", err)
				continue
			}
			inventoryHostsMapWithIp[hostName].Host.Progress.CurrentStage = status
		}
	}
}

func GetPodInStatus(k8Client k8s_client.K8SClient, podNamePrefix string, namespace string, labelMatch map[string]string,
	status v1.PodPhase, log logrus.FieldLogger) *v1.Pod {

	pods, err := k8Client.GetPods(namespace, labelMatch, "")
	if err != nil {
		log.WithError(err).Warnf("Failed to get pod with prefix %s in namespace %s", podNamePrefix, namespace)
		return nil
	}
	log.Debugf("Got %d pods, will search for one with given prefix %s", len(pods), podNamePrefix)
	for _, pod := range pods {
		if strings.HasPrefix(pod.Name, podNamePrefix) && pod.Status.Phase == status {
			log.Infof("Found pod %s in status %s", pod.Name, status)
			return &pod
		} else if strings.HasPrefix(pod.Name, podNamePrefix) {
			log.Infof("Found pod %s in status %s", pod.Name, status)
			return nil
		}
	}
	return nil
}

// get pod logs,
// write tar.gz to pipe in a routine
// upload tar.gz from pipe to assisted service.
// close read and write pipes
func UploadPodLogs(kc k8s_client.K8SClient, ic inventory_client.InventoryClient, clusterId string, podName string, namespace string,
	sinceSeconds int64, log logrus.FieldLogger) error {
	log.Infof("Uploading logs for %s in %s", podName, namespace)
	podLogs, err := kc.GetPodLogsAsBuffer(namespace, podName, sinceSeconds)
	if err != nil {
		podLogs = &bytes.Buffer{}
		podLogs.WriteString(errors.Wrapf(err, "Failed to get logs of pod %s", podName).Error())
	}
	pr, pw := io.Pipe()
	defer pr.Close()

	go func() {
		defer pw.Close()
		tarEntry := utils.NewTarEntry(podLogs, nil, int64(podLogs.Len()), fmt.Sprintf("%s.logs", podName))
		err = utils.WriteToTarGz(pw, []utils.TarEntry{*tarEntry})
		if err != nil {
			log.WithError(err).Warnf("Failed to create tar.gz")
		}
	}()
	// if error will occur in goroutine above
	//it will close writer and upload will fail
	ctx := utils.GenerateRequestContext()
	err = ic.UploadLogs(ctx, clusterId, models.LogsTypeController, pr)
	log.Infof("Done uploading controller logs to the service")
	if err != nil {
		utils.RequestIDLogger(ctx, log).WithError(err).Error("Failed to upload logs")
		return errors.Wrapf(err, "Failed to upload logs")
	}
	return nil
}

func IsK8sNodeIsReady(node v1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == v1.NodeReady && cond.Status == v1.ConditionTrue {
			return true
		}
	}
	return false
}

// BuildHostsMapIPAddressBased builds a map containing all the IP addresses of the hosts in the
// inventory so that later we can match reporting hosts based on the IP and not only on the name.
func BuildHostsMapIPAddressBased(inventoryHostsMap map[string]inventory_client.HostData) map[string]inventory_client.HostData {
	knownIpAddresses := map[string]inventory_client.HostData{}
	for _, v := range inventoryHostsMap {
		for _, ip := range v.IPs {
			knownIpAddresses[ip] = v
		}
	}
	return knownIpAddresses
}

// Matching of the host happens based on 2 rules
//   * if the name of the host and in the inventory is exactly the same, use use it
//   * if the name is not known in the inventory, we check if the IP address of the
//     reporting host is known to the inventory
// Using those rules we can cover the cases where e.g. inventory expects a short
// hostname, but the host reports itself using its FQDN
func HostMatchByNameOrIPAddress(node v1.Node, namesMap, IPAddressMap map[string]inventory_client.HostData) (inventory_client.HostData, bool) {
	host, ok := namesMap[strings.ToLower(node.Name)]
	if !ok {
		for _, ip := range node.Status.Addresses {
			_, exists := IPAddressMap[ip.Address]
			if exists && ip.Type == v1.NodeInternalIP {
				ok = true
				host = IPAddressMap[ip.Address]
			}
		}
	}
	return host, ok
}
