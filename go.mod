module github.com/openshift/assisted-installer

go 1.16

require (
	github.com/Microsoft/go-winio v0.4.15-0.20200113171025-3fe6c5262873 // indirect
	github.com/PuerkitoBio/rehttp v1.1.0
	github.com/ReneKroon/ttlcache/v2 v2.11.0
	github.com/coreos/ignition/v2 v2.13.0
	github.com/go-openapi/runtime v0.23.1
	github.com/go-openapi/strfmt v0.21.2
	github.com/go-openapi/swag v0.21.1
	github.com/golang/mock v1.6.0
	github.com/google/uuid v1.3.0
	github.com/hashicorp/go-version v1.4.0
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/metal3-io/baremetal-operator v0.0.0
	github.com/onsi/ginkgo v1.16.5
	github.com/onsi/gomega v1.18.1
	github.com/openshift/api v3.9.1-0.20191111211345-a27ff30ebf09+incompatible
	github.com/openshift/assisted-installer-agent v0.0.0-20200811180147-bc9c7b899b8a
	github.com/openshift/assisted-service v1.0.10-0.20220116113517-db25501e204a
	github.com/openshift/client-go v0.0.0-20201020074620-f8fd44879f7c
	github.com/openshift/machine-api-operator v0.2.1-0.20201002104344-6abfb5440597
	github.com/operator-framework/api v0.13.0
	github.com/operator-framework/operator-lifecycle-manager v0.18.0
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.8.1
	github.com/thoas/go-funk v0.9.1
	github.com/vincent-petithory/dataurl v1.0.0
	golang.org/x/net v0.0.0-20220127200216-cd36cc0744dd
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.22.1
	k8s.io/apimachinery v0.22.1
	k8s.io/client-go v12.0.0+incompatible
	sigs.k8s.io/controller-runtime v0.10.0
)

replace (
	github.com/irifrance/gini => github.com/go-air/gini v1.0.1
	github.com/metal3-io/baremetal-operator => github.com/openshift/baremetal-operator v0.0.0-20210409032903-31b989a197eb // Use OpenShift fork

	github.com/openshift/api => github.com/openshift/api v0.0.0-20200901182017-7ac89ba6b971
	github.com/openshift/hive/pkg/apis => github.com/carbonin/hive/pkg/apis v0.0.0-20210209195732-57e8c3ae12d1
	github.com/openshift/machine-api-operator => github.com/openshift/machine-api-operator v0.2.1-0.20201026110925-50ea569da51b
	k8s.io/api => k8s.io/api v0.19.2
	k8s.io/apimachinery => k8s.io/apimachinery v0.19.2
	k8s.io/client-go => k8s.io/client-go v0.19.2
	k8s.io/code-generator => k8s.io/code-generator v0.0.0-20200214080538-dc8f3adce97c
	sigs.k8s.io/cluster-api-provider-aws => github.com/openshift/cluster-api-provider-aws v0.2.1-0.20201022175424-d30c7a274820
	sigs.k8s.io/cluster-api-provider-azure => github.com/openshift/cluster-api-provider-azure v0.1.0-alpha.3.0.20201016155852-4090a6970205
	sigs.k8s.io/controller-runtime => sigs.k8s.io/controller-runtime v0.5.1-0.20200330174416-a11a908d91e0
)
