# Undermoon Operator ![Continuous Integration](https://github.com/doyoubi/undermoon-operator/workflows/Continuous%20Integration/badge.svg)
Kubernetes operator for easy `Redis Cluster` management
based on [undermoon](https://github.com/doyoubi/undermoon)
using [operator-sdk](https://sdk.operatorframework.io/).

## Usage

### Run the Operator
Run the `undermoon-operator`:
Note that you can change the name `my-undermoon-operator`.
```
helm install my-undermoon-operator helm/undermoon-operator
```

### Create an Undermoon Cluster
Create an undermoon cluster by installing helm charts package:
```
helm install \
    --set 'cluster.clusterName=my-cluster-name' \
    --set 'cluster.chunkNumber=1' \
    --set 'cluster.maxMemory=2048' \
    --set 'cluster.port=5299' \
    my-cluster \
    -n my-namespace \
    helm/undermoon-cluster
```

Fields here:
- `clusterName`: Name of the cluster. Should be less than 30 bytes.
    This can't be modifed.
- `chunkNumber`: Used to specify the node number of the cluster.
    One chunk always consists of 2 masters and 2 replicas.
    Modify this to scale the cluster.
- `maxMemory`: Specifies the `maxmemory` config for each Redis node in MBs.
    Modify this will trigger rolling upgrade.
- `port`: The service port your redis clients connect to.
    This can't be modifed.

Then you can access the service through `my-cluster:5299` inside the Kubernetes cluster:
```
# This can only be run inside the Kubernetes cluster.
redis-cli -h my-cluster.my-namespace.svc.cluster.local -p 5299 -c get mykey
```

### Scale the Cluster
```
kubectl edit undermoon/my-cluster
# Change the `chunkNumber`, save, and exit.
```
Then the cluster will automatically scale the cluster.

### Using undermoon-scheduler
By default, undermoon does not guarantee master and replica redis instances in the same shard are not in the same node.
We need to use `undermoon-scheduler` based on [scheduler framework](https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/) to achieve that.

This will install `undermoon-scheduler` as a second scheduler.
You may want to replace the default scheduler instead.
See the [docs](https://github.com/kubernetes-sigs/scheduler-plugins/blob/master/doc/install.md#as-a-second-scheduler) from scheduler-plugins for more details.

```
helm install example-scheduler -n my-namespace helm/undermoon-scheduler
```

Then specify the `schedulerName` when installing `undermoon-cluster`:
```
helm install \
    --set 'cluster.clusterName=my-cluster-name' \
    --set 'cluster.chunkNumber=1' \
    --set 'cluster.maxMemory=2048' \
    --set 'cluster.port=5299' \
    --set "schedulerName=undermoon-scheduler" \
    my-cluster \
    -n my-namespace \
    helm/undermoon-cluster
```

## Docs
- [Development](./docs/development.md)
