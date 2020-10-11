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
)

var errMigrationRunning = errors.New("MIGRATION_RUNNING")
var errFreeNodeFound = errors.New("FREE_NODE_FOUND")

type errorResponse struct {
	Error string `json:"error"`
}

type brokerClient struct {
	httpClient *resty.Client
}

func newBrokerClient() *brokerClient {
	httpClient := resty.New()
	httpClient.SetHeader("Content-Type", "application/json")
	return &brokerClient{
		httpClient: httpClient,
	}
}

func (client *brokerClient) getReplicaAddresses(address string) ([]string, error) {
	url := fmt.Sprintf("http://%s/api/v2/config", address)
	payload := brokerConfigPayload{}
	res, err := client.httpClient.R().SetResult(payload).Get(url)
	if err != nil {
		return nil, err
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
	url := fmt.Sprintf("http://%s/api/v2/epoch", address)
	res, err := client.httpClient.R().Get(url)
	if err != nil {
		return 0, err
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
	url := fmt.Sprintf("http://%s/api/v2/proxies/addresses", address)
	res, err := client.httpClient.R().SetResult(&queryServerProxyResponse{}).Get(url)
	if err != nil {
		return nil, err
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
	url := fmt.Sprintf("http://%s/api/v2/config", address)
	payload := brokerConfigPayload{
		ReplicaAddresses: replicaAddresses,
	}
	res, err := client.httpClient.R().SetBody(&payload).Put(url)
	if err != nil {
		return err
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
	url := fmt.Sprintf("http://%s/api/v2/proxies/meta", address)
	res, err := client.httpClient.R().SetBody(&proxy).Post(url)
	if err != nil {
		return err
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
	url := fmt.Sprintf("http://%s/api/v2/proxies/meta/%s", address, proxyAddress)
	res, err := client.httpClient.R().Delete(url)
	if err != nil {
		return err
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
	url := fmt.Sprintf("http://%s/api/v2/clusters/meta/%s", address, clusterName)
	payload := &createClusterPayload{
		NodeNumber: chunkNumber * chunkNodeNumber,
	}
	res, err := client.httpClient.R().SetBody(payload).SetError(&errorResponse{}).Post(url)
	if err != nil {
		return err
	}

	if res.StatusCode() == 200 {
		return nil
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
	url := fmt.Sprintf("http://%s/api/v2/clusters/names", address)
	res, err := client.httpClient.R().SetResult(&queryClusterNamesPayload{}).Get(url)
	if err != nil {
		return false, err
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
	url := fmt.Sprintf("http://%s/api/v2/clusters/migrations/auto/%s/%d", address, clusterName, nodeNumber)
	res, err := client.httpClient.R().SetError(&errorResponse{}).Post(url)
	if err != nil {
		return err
	}

	if res.StatusCode() == 200 {
		return nil
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
	url := fmt.Sprintf("http://%s/api/v2/clusters/free_nodes/%s", address, clusterName)
	res, err := client.httpClient.R().SetError(&errorResponse{}).Delete(url)
	if err != nil {
		return err
	}

	if res.StatusCode() == 200 {
		return nil
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
	url := fmt.Sprintf("http://%s/api/v2/clusters/info/%s", address, clusterName)
	res, err := client.httpClient.R().SetResult(&clusterInfo{}).SetError(&errorResponse{}).Get(url)
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

	if res.StatusCode() == 404 {
		response, ok := res.Error().(*errorResponse)
		if ok {
			return nil, errors.Errorf("cluster info not found, error code %s", response.Error)
		}
	}

	content := res.Body()
	return nil, errors.Errorf("Failed to get cluster info: invalid status code %d: %s", res.StatusCode(), string(content))
}
