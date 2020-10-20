package common

import (
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
	v1 "k8s.io/api/core/v1"
)

const ControllerLogsSecondsAgo = 60 * 60

func SetConfiguringStatusForHosts(client inventory_client.InventoryClient, inventoryHostsMapWithIp map[string]inventory_client.HostData,
	mcsLogs string, fromBootstrap bool, log *logrus.Logger) {
	notValidStates := map[models.HostStage]struct{}{models.HostStageConfiguring: {}, models.HostStageJoined: {}, models.HostStageDone: {}}
	if fromBootstrap {
		notValidStates[models.HostStageWaitingForIgnition] = struct{}{}
	}
	for key, host := range inventoryHostsMapWithIp {
		_, ok := notValidStates[host.Host.Progress.CurrentStage]
		if ok {
			continue
		}
		log.Infof("Verifying if host %s pulled ignition", key)
		pat := fmt.Sprintf("(%s)", strings.Join(host.IPs, "|"))
		pattern, err := regexp.Compile(pat)
		if err != nil {
			log.WithError(err).Errorf("Failed to compile regex from host %s ips list", host.Host.ID.String())
			return
		}
		if pattern.MatchString(mcsLogs) {
			status := models.HostStageConfiguring
			if fromBootstrap && host.Host.Role == models.HostRoleWorker {
				status = models.HostStageWaitingForIgnition
			}
			log.Infof("Host %s found in mcs logs, moving it to %s state", host.Host.ID.String(), status)
			if err := client.UpdateHostInstallProgress(host.Host.ID.String(), status, ""); err != nil {
				log.Errorf("Failed to update node installation status, %s", err)
				continue
			}
			inventoryHostsMapWithIp[key].Host.Progress.CurrentStage = status
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
		return errors.Wrapf(err, "Failed to get logs of pod %s", podName)
	}
	pr, pw := io.Pipe()
	defer pr.Close()

	go func() {
		defer pw.Close()
		err = utils.WriteToTarGz(pw, podLogs, int64(podLogs.Len()), fmt.Sprintf("%s.logs", podName))
		if err != nil {
			log.WithError(err).Warnf("Failed to create tar.gz")
		}
	}()
	// if error will occur in goroutine above
	//it will close writer and upload will fail
	err = ic.UploadLogs(clusterId, models.LogsTypeController, pr)
	if err != nil {
		return errors.Wrapf(err, "Failed to upload logs")
	}
	return nil
}
