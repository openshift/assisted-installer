module github.com/openshift/assisted-installer

go 1.14

require (
	github.com/Microsoft/go-winio v0.4.15-0.20200113171025-3fe6c5262873 // indirect
	github.com/PuerkitoBio/rehttp v1.0.0
	github.com/aybabtme/iocontrol v0.0.0-20150809002002-ad15bcfc95a0 // indirect
	github.com/benbjohnson/clock v1.0.3 // indirect
	github.com/coreos/ignition/v2 v2.9.0
	github.com/docker/docker v17.12.0-ce-rc1.0.20200505174321-1655290016ac+incompatible // indirect
	github.com/go-openapi/runtime v0.19.27
	github.com/go-openapi/strfmt v0.20.0
	github.com/golang/mock v1.5.0
	github.com/google/uuid v1.2.0
	github.com/hashicorp/go-version v1.2.1
	github.com/jpillora/backoff v1.0.0
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/metal3-io/baremetal-operator v0.0.0
	github.com/onsi/ginkgo v1.15.2
	github.com/onsi/gomega v1.11.0
	github.com/openshift/api v3.9.1-0.20191111211345-a27ff30ebf09+incompatible
	github.com/openshift/assisted-installer-agent v0.0.0-20200811180147-bc9c7b899b8a
	github.com/openshift/assisted-service v1.0.10-0.20210310114450-f60ae14adc85
	github.com/openshift/client-go v0.0.0-20201020074620-f8fd44879f7c
	github.com/openshift/machine-api-operator v0.2.1-0.20201002104344-6abfb5440597
	github.com/operator-framework/api v0.7.1
	github.com/operator-framework/operator-lifecycle-manager v0.17.0
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.8.1
	github.com/thoas/go-funk v0.8.0
	github.com/vincent-petithory/dataurl v0.0.0-20191104211930-d1553a71de50
	golang.org/x/net v0.0.0-20201202161906-c7110b5ffcbb
	golang.org/x/sync v0.0.0-20201020160332-67f06af15bc9
	gopkg.in/yaml.v2 v2.4.0
	k8s.io/api v0.20.4
	k8s.io/apimachinery v0.20.4
	k8s.io/client-go v12.0.0+incompatible
	sigs.k8s.io/controller-runtime v0.8.3
)

replace (
	github.com/metal3-io/baremetal-operator => github.com/openshift/baremetal-operator v0.0.0-20200715132148-0f91f62a41fe // Use OpenShift fork
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
