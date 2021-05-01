package controllers

import (
	"fmt"
	"strconv"

	"github.com/go-resty/resty/v2"
	"github.com/pkg/errors"
)

const (
	errStrAlreadyExists       = "ALREADY_EXISTED"
	errStrMigrationRunning    = "MIGRATION_RUNNING"
	errStrNoAvailableResource = "NO_AVAILABLE_RESOURCE"
	errStrFreeNodeFound       = "FREE_NODE_FOUND"
	errStrFreeNodeNotFound    = "FREE_NODE_NOT_FOUND"
	errStrRetry               = "RETRY"
	errStrExternalTimeout     = "EXTERNAL_TIMEOUT"
)

var (
	errMigrationRunning = errors.New("MIGRATION_RUNNING")
	errFreeNodeFound    = errors.New("FREE_NODE_FOUND")
	errExternalTimeout  = errors.New("EXTERNAL_TIMEOUT")
)

type errorResponse struct {
	Error string `json:"error"`
}

type brokerClient struct {
	httpClient *resty.Client
	apiVersion string
}

func newBrokerClient(apiVersion string) *brokerClient {
	httpClient := resty.New()
	httpClient.SetHeader("Content-Type", "application/json")
	return &brokerClient{
		httpClient: httpClient,
		apiVersion: apiVersion,
	}
}

func (client *brokerClient) getReplicaAddresses(address string) ([]string, error) {
	url := fmt.Sprintf("http://%s/api/%s/config", address, client.apiVersion)
	payload := brokerConfigPayload{}
	res, err := client.httpClient.R().
		SetResult(payload).
		SetError(&errorResponse{}).
		Get(url)
	if err != nil {
		return nil, err
	}

	if res.StatusCode() == 504 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrExternalTimeout {
			return nil, errExternalTimeout
		}
	}

	if res.StatusCode() != 200 {
		return nil, errors.Errorf("Failed to get replica addresses from broker: invalid status code %d", res.StatusCode())
	}

	resPayload, ok := res.Result().(*brokerConfigPayload)
	if !ok {
		content := res.Body()
		return nil, errors.Errorf("Failed to get replica addresses from broker: invalid response payload %s", string(content))
	}

	addresses := resPayload.ReplicaAddresses
	return addresses, nil
}

func (client *brokerClient) getEpoch(address string) (int64, error) {
	url := fmt.Sprintf("http://%s/api/%s/epoch", address, client.apiVersion)
	res, err := client.httpClient.R().SetError(&errorResponse{}).Get(url)
	if err != nil {
		return 0, err
	}

	if res.StatusCode() == 504 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrExternalTimeout {
			return 0, errExternalTimeout
		}
	}

	if res.StatusCode() != 200 {
		return 0, errors.Errorf("Failed to get broker epoch: invalid status code %d", res.StatusCode())
	}

	body := res.Body()
	epoch, err := strconv.ParseInt(string(body), 10, 64)
	if err != nil {
		return 0, errors.Errorf("Invalid epoch from broker: %s", string(body))
	}

	return epoch, nil
}

type queryServerProxyResponse struct {
	Addresses []string `json:"addresses"`
}

func (client *brokerClient) getServerProxies(address string) ([]string, error) {
	url := fmt.Sprintf("http://%s/api/%s/proxies/addresses", address, client.apiVersion)
	res, err := client.httpClient.R().
		SetResult(&queryServerProxyResponse{}).
		SetError(&errorResponse{}).
		Get(url)
	if err != nil {
		return nil, err
	}

	if res.StatusCode() == 504 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrExternalTimeout {
			return nil, errExternalTimeout
		}
	}

	if res.StatusCode() != 200 {
		content := res.Body()
		return nil, errors.Errorf("Failed to register server proxy: invalid status code %d: %s", res.StatusCode(), string(content))
	}

	resultPayload := res.Result().(*queryServerProxyResponse)
	return resultPayload.Addresses, nil
}

type serverProxyMeta struct {
	ProxyAddress   string    `json:"proxy_address"`
	RedisAddresses [2]string `json:"nodes"`
	Host           string    `json:"host"`
	Index          int       `json:"index"`
}

func newServerProxyMeta(podHost, nodeIP string, serverProxyPort uint32, index int) serverProxyMeta {
	return serverProxyMeta{
		ProxyAddress: fmt.Sprintf("%s:%d", podHost, serverProxyPort),
		RedisAddresses: [2]string{
			fmt.Sprintf("%s:%d", podHost, redisPort1),
			fmt.Sprintf("%s:%d", podHost, redisPort2),
		},
		Host:  nodeIP,
		Index: index,
	}
}

type brokerConfigPayload struct {
	ReplicaAddresses []string `json:"replica_addresses"`
}

func (client *brokerClient) setBrokerReplicas(address string, replicaAddresses []string) error {
	url := fmt.Sprintf("http://%s/api/%s/config", address, client.apiVersion)
	payload := brokerConfigPayload{
		ReplicaAddresses: replicaAddresses,
	}
	res, err := client.httpClient.R().
		SetBody(&payload).
		SetError(&errorResponse{}).
		Put(url)
	if err != nil {
		return err
	}

	if res.StatusCode() == 504 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrExternalTimeout {
			return errExternalTimeout
		}
	}

	if res.StatusCode() == 409 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrRetry {
			return errRetryReconciliation
		}
	}

	if res.StatusCode() != 200 {
		content := res.Body()
		return errors.Errorf("Failed to register server proxy: invalid status code %d: %s", res.StatusCode(), string(content))
	}

	return nil
}

func (client *brokerClient) registerServerProxy(address string, proxy serverProxyMeta) error {
	url := fmt.Sprintf("http://%s/api/%s/proxies/meta", address, client.apiVersion)
	res, err := client.httpClient.R().
		SetBody(&proxy).
		SetError(&errorResponse{}).
		Post(url)
	if err != nil {
		return err
	}

	if res.StatusCode() == 504 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrExternalTimeout {
			return errExternalTimeout
		}
	}

	if res.StatusCode() == 409 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrRetry {
			return errRetryReconciliation
		}
	}

	if res.StatusCode() != 200 && res.StatusCode() != 409 {
		content := res.Body()
		return errors.Errorf("Failed to register server proxy: invalid status code %d: %s", res.StatusCode(), string(content))
	}

	return nil
}

func (client *brokerClient) deregisterServerProxy(address string, proxyAddress string) error {
	url := fmt.Sprintf("http://%s/api/%s/proxies/meta/%s", address, client.apiVersion, proxyAddress)
	res, err := client.httpClient.R().SetError(&errorResponse{}).Delete(url)
	if err != nil {
		return err
	}

	if res.StatusCode() == 504 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrExternalTimeout {
			return errExternalTimeout
		}
	}

	if res.StatusCode() == 409 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrRetry {
			return errRetryReconciliation
		}
	}

	if res.StatusCode() != 200 && res.StatusCode() != 404 {
		content := res.Body()
		return errors.Errorf("Failed to deregister server proxy: invalid status code %d: %s", res.StatusCode(), string(content))
	}

	return nil
}

type createClusterPayload struct {
	NodeNumber int `json:"node_number"`
}

func (client *brokerClient) createCluster(address, clusterName string, chunkNumber int) error {
	url := fmt.Sprintf("http://%s/api/%s/clusters/meta/%s", address, client.apiVersion, clusterName)
	payload := &createClusterPayload{
		NodeNumber: chunkNumber * chunkNodeNumber,
	}
	res, err := client.httpClient.R().
		SetBody(payload).
		SetError(&errorResponse{}).
		Post(url)
	if err != nil {
		return err
	}

	if res.StatusCode() == 200 {
		return nil
	}

	if res.StatusCode() == 504 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrExternalTimeout {
			return errExternalTimeout
		}
	}

	if res.StatusCode() == 409 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrNoAvailableResource {
			return errRetryReconciliation
		}
		if ok && response.Error == errStrAlreadyExists {
			return nil
		}
		if ok && response.Error == errStrRetry {
			return errRetryReconciliation
		}
	}

	content := res.Body()
	return errors.Errorf("Failed to register server proxy: invalid status code %d: %s", res.StatusCode(), string(content))
}

type queryClusterNamesPayload struct {
	Names []string `json:"names"`
}

func (client *brokerClient) clusterExists(address, clusterName string) (bool, error) {
	url := fmt.Sprintf("http://%s/api/%s/clusters/names", address, client.apiVersion)
	res, err := client.httpClient.R().
		SetResult(&queryClusterNamesPayload{}).
		SetError(&errorResponse{}).
		Get(url)
	if err != nil {
		return false, err
	}

	if res.StatusCode() == 504 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrExternalTimeout {
			return false, errExternalTimeout
		}
	}

	if res.StatusCode() != 200 {
		content := res.Body()
		return false, errors.Errorf("Failed to register server proxy: invalid status code %d: %s", res.StatusCode(), string(content))
	}

	response, ok := res.Result().(*queryClusterNamesPayload)
	if !ok {
		content := res.Body()
		return false, errors.Errorf("Failed to get cluster names: invalid response payload %s", string(content))
	}

	for _, name := range response.Names {
		if name == clusterName {
			return true, nil
		}
	}
	return false, nil
}

func (client *brokerClient) scaleNodes(address, clusterName string, chunkNumber int) error {
	nodeNumber := chunkNumber * chunkNodeNumber
	url := fmt.Sprintf("http://%s/api/%s/clusters/migrations/auto/%s/%d",
		address, client.apiVersion, clusterName, nodeNumber)
	res, err := client.httpClient.R().SetError(&errorResponse{}).Post(url)
	if err != nil {
		return err
	}

	if res.StatusCode() == 200 {
		return nil
	}

	if res.StatusCode() == 504 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrExternalTimeout {
			return errExternalTimeout
		}
	}

	if res.StatusCode() == 409 || res.StatusCode() == 400 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrMigrationRunning {
			return errMigrationRunning
		}
		if ok && response.Error == errStrFreeNodeFound {
			return errFreeNodeFound
		}
		if ok && response.Error == errStrRetry {
			return errRetryReconciliation
		}
	}

	content := res.Body()
	return errors.Errorf("Failed to change node number: invalid status code %d: %s", res.StatusCode(), string(content))
}

func (client *brokerClient) removeFreeNodes(address, clusterName string) error {
	url := fmt.Sprintf("http://%s/api/%s/clusters/free_nodes/%s", address, client.apiVersion, clusterName)
	res, err := client.httpClient.R().SetError(&errorResponse{}).Delete(url)
	if err != nil {
		return err
	}

	if res.StatusCode() == 200 {
		return nil
	}

	if res.StatusCode() == 504 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrExternalTimeout {
			return errExternalTimeout
		}
	}

	if res.StatusCode() == 404 || res.StatusCode() == 409 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrMigrationRunning {
			return errMigrationRunning
		}
		if ok && response.Error == errStrFreeNodeNotFound {
			return nil
		}
		if ok && response.Error == errStrRetry {
			return errRetryReconciliation
		}
	}

	content := res.Body()
	return errors.Errorf("Failed to remove free nodes: invalid status code %d: %s", res.StatusCode(), string(content))
}

type clusterInfo struct {
	Name                string `json:"name"`
	NodeNumber          int    `json:"node_number"`
	NodeNumberWithSlots int    `json:"node_number_with_slots"`
	IsMigrating         bool   `json:"is_migrating"`
}

func (client *brokerClient) getClusterInfo(address, clusterName string) (*clusterInfo, error) {
	url := fmt.Sprintf("http://%s/api/%s/clusters/info/%s", address, client.apiVersion, clusterName)
	res, err := client.httpClient.R().
		SetResult(&clusterInfo{}).
		SetError(&errorResponse{}).
		Get(url)
	if err != nil {
		return nil, err
	}

	if res.StatusCode() == 200 {
		info, ok := res.Result().(*clusterInfo)
		if !ok {
			return nil, errors.Errorf("failed to get cluster info, invalid content %s", res.Body())
		}
		return info, nil
	}

	if res.StatusCode() == 504 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrExternalTimeout {
			return nil, errExternalTimeout
		}
	}

	if res.StatusCode() == 404 {
		response, ok := res.Error().(*errorResponse)
		if ok {
			return nil, errors.Errorf("cluster info not found, error code %s", response.Error)
		}
	}

	content := res.Body()
	return nil, errors.Errorf("Failed to get cluster info: invalid status code %d: %s", res.StatusCode(), string(content))
}

type clusterConfigPayload struct {
	MigrationScanInterval string `json:"migration_scan_interval"`
	MigrationScanCount    string `json:"migration_scan_count"`
}

func (client *brokerClient) changeClusterConfig(address, clusterName string, scanInterval, scanCount uint32) error {
	url := fmt.Sprintf("http://%s/api/%s/clusters/config/%s", address, client.apiVersion, clusterName)
	payload := &clusterConfigPayload{
		MigrationScanInterval: fmt.Sprintf("%d", scanInterval),
		MigrationScanCount:    fmt.Sprintf("%d", scanCount),
	}
	res, err := client.httpClient.R().
		SetBody(payload).
		SetError(&errorResponse{}).
		Patch(url)
	if err != nil {
		return err
	}

	if res.StatusCode() == 200 {
		return nil
	}

	if res.StatusCode() == 504 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrExternalTimeout {
			return errExternalTimeout
		}
	}

	if res.StatusCode() == 404 {
		response, ok := res.Error().(*errorResponse)
		if ok {
			return errors.Errorf("cluster not found, error code %s", response.Error)
		}
	}

	if res.StatusCode() == 409 {
		response, ok := res.Error().(*errorResponse)
		if ok && response.Error == errStrRetry {
			return errRetryReconciliation
		}
		if ok && response.Error == errStrMigrationRunning {
			return errMigrationRunning
		}
	}

	content := res.Body()
	return errors.Errorf("Failed to update cluster config: invalid status code %d: %s", res.StatusCode(), string(content))
}
