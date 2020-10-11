# Development

## Prerequisites
Need to install [operator-sdk](https://sdk.operatorframework.io/) first.

## Basics

After modifying the `undermoon_types.go` file,
run the following to generate codes:
```
> make update-types
```

linter:
```
> make lint
```

Use [kind](https://kind.sigs.k8s.io/) to run Kubernetes locally.
```
> make kind-env
```

This will also create a registry on `localhost:5000`.

When you need to remove:
```
> make kind-env-stop
```

## Images

See the uploaded images:
```
> make list-images
```

### Undermoon Image
Run this to tell Makefile that we're developing locally:
```
> touch debug
```

The undermoon image should be renamed and pushed to `localhost:5000/undermoon_test`.

Build from undermoon repository:
```
> git clone https://github.com/doyoubi/undermoon
> cd undermoon
> make docker-build-test-image
> docker tag undermoon_test:latest localhost:5000/undermoon_test
> docker push localhost:5000/undermoon_test
```

### Operator Image
```
> make debug-build
```

## E2E Tests (Not Ready)

Run e2e tests:
```
> make e2e-test
```

Clean up the environment when the test fails:
```
> make cleanup-e2e-test
```

## Run Operator For Debugging

Run the operator in Kubernetes:
```
> make debug-run
```

Create the Undermoon CRD:
```
> make debug-start
```

Or just:
```
> make debug-install
```

See the pods we created:
```
> kubectl get pods
```

See the logs of the operator:
```
> make debug-logs
```

Change the Undermoon CRD we created:
```
> make debug-edit
```

Cleanup the environment after debugging:
```
> make debug-stop
```

### Tools
Run an image with `curl` and `jq` to debug the broker:
```
> make run-jq-curl
```

Run `redis-cli` to access the redis cluster service:
```
> make run-redis-cli
```

## Helm Charts and Chaos Test

The chao test uses Helm Charts package.

There's a checker service that will keep accessing the cluster
and check the data.
Note that due to limited resources in PC,
failover could happen and cause `INCONSISTENT DATA` error.

Build the checker images:
```
> make docker-build-checker-image
```

Install the operator and create the Undermoon CRD:
```
> make install-helm-package
```

Run the checker that stops on error:
```
> make install-undermoon-checker
```

Or run the checker that doesn't stop on error and keeps printing success rate:
```
> make install-undermoon-chaos-checker
```

Get the logs of the checker:
```
> make checker-logs
```

Then we can keep scaling the cluster and create some failures:
```
> pip install -r chaostest/ctrl_requirements.txt

# Run without killing pods
> make test-ctrl

# Run with killing pods
> make test-chaos-ctrl
```

Stop the checker
```
> make uninstall-undermoon-checker
```

Remove the Undermoon CRD and operator:
```
> make uninstall-helm-package
```

