# Undermoon Operator
Kubernetes operator for easy `Redis Cluster` management
based on [undermoon](https://github.com/doyoubi/undermoon)
using [operator-sdk](https://sdk.operatorframework.io/).

## Usage

### Build Helm Charts
```
> make build-helm
```

Then you can see the following packages in the current directory:
- undermoon-operator-0.1.0.tgz
- undermoon-cluster-0.1.0.tgz

### Run the Operator
Run the `undermoon-operator`:
Note that you can change the name `my-undermoon-operator`.
```
> helm install my-undermoon-operator undermoon-operator-0.1.0.tgz
```

### Create an Undermoon Cluster
Create an undermoon cluster by installing helm charts package:
```
> helm install my-cluster \
    --set 'cluster.clusterName=my-cluster-name' \
    --set 'cluster.chunkNumber=1' \
    --set 'cluster.maxMemory=50' \
    --set 'cluster.port=5299' \
    undermoon-cluster-0.1.0.tgz
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
> redis-cli -h my-cluster -p 5299 -c get mykey
```

### Scale the Cluster
```
> kubectl edit undermoon/my-cluster
# Change the `chunkNumber`, save, and exit.
```
Then the cluster will automatically scale the cluster.

## Docs
- [Development](./docs/development.md)
