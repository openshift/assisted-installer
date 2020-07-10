package common

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/eranco74/assisted-installer/src/inventory_client"
	"github.com/filanov/bm-inventory/models"
	"github.com/sirupsen/logrus"
)

func SetConfiguringStatusForHosts(client inventory_client.InventoryClient, inventoryHostsMapWithIp map[string]inventory_client.EnabledHostData,
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
