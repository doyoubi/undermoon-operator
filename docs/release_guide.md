# Release Guide

Need to change the following versions:

[Makefile](../Makefile)
```
VERSION ?= vx.x.x
```

[Makefile.utils](../Makefile.utils)
```
OPERATOR_HELM_VERSION=0.3.1
```

[README.md](../README.md)
Need to change all the
- `undermoon-operator-x.x.x.tgz`
- `undermoon-cluster-x.x.x.tgz`
- `undermoon-scheduler-x.x.x.tgz`

[config/manager/overlays/test/kustomization.yaml](../config/manager/overlays/test/kustomization.yaml)
```
images:
- name: controller
  newName: localhost:5000/undermoon-operator
  newTag: vx.x.x
```

[helm/undermoon-cluster/Chart.yaml ](../helm/undermoon-cluster/Chart.yaml)
```
version: x.x.x
```

[helm/undermoon-operator/Chart.yaml](../helm/undermoon-operator/Chart.yaml)
```
version: x.x.x
...
appVersion: vx.x.x
```

[helm/undermoon-operator/values.yaml](../helm/undermoon-operator/values.yaml)
```
image:
  operatorImage: doyoubi/undermoon-operator
  operatorImageTag: vx.x.x
```

[helm/undermoon-scheduler/Chart.yaml](../helm/undermoon-scheduler/Chart.yaml)
```
version: x.x.x
...
# If scheduler image is updated
appVersion: vx.x.x
```

Then finally run:
```
make update-types
```
to make sure everything updated.
