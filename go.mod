module github.com/doyoubi/undermoon-operator

go 1.13

require (
	github.com/gin-gonic/gin v1.6.3
	github.com/go-logr/logr v0.1.0
	github.com/go-redis/redis/v8 v8.0.0-beta.7
	github.com/go-resty/resty/v2 v2.3.0
	github.com/onsi/ginkgo v1.12.1
	github.com/onsi/gomega v1.10.1
	github.com/pkg/errors v0.8.1
	github.com/sethvargo/go-password v0.2.0
	github.com/stretchr/testify v1.6.1
	golang.org/x/sync v0.0.0-20190911185100-cd5d95a43a6e
	k8s.io/api v0.18.6
	k8s.io/apimachinery v0.18.6
	k8s.io/client-go v0.18.6
	sigs.k8s.io/controller-runtime v0.6.4
)
