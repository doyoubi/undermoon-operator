module github.com/doyoubi/undermoon-operator

go 1.13

require (
	github.com/go-logr/logr v0.1.0
	github.com/go-redis/redis/v8 v8.0.0-beta.7
	github.com/go-resty/resty/v2 v2.3.0
	github.com/onsi/ginkgo v1.12.1
	github.com/onsi/gomega v1.10.1
	github.com/pkg/errors v0.8.1
	github.com/stretchr/testify v1.6.1
	k8s.io/api v0.18.6
	k8s.io/apimachinery v0.18.6
	k8s.io/client-go v0.18.6
	sigs.k8s.io/controller-runtime v0.6.2
)
