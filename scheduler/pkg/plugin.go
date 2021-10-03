package pkg

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

const (
	// PluginName specifies the plugin name
	PluginName                  = "undermoon-topology"
	undermoonServiceTypeStorage = "storage"
)

var _ framework.FilterPlugin = &UndermoonTopology{}

type UndermoonTopology struct {
}

func New(plArgs runtime.Object, handle framework.Handle) (framework.Plugin, error) {
	klog.V(0).Infof("Plugin args: %+v", plArgs)
	return &UndermoonTopology{}, nil
}

func (s *UndermoonTopology) Name() string {
	return PluginName
}

// TODO: Need locking for a undermoon cluster
func (s *UndermoonTopology) Filter(ctx context.Context, state *framework.CycleState, pod *v1.Pod, node *framework.NodeInfo) *framework.Status {
	serviceType, ok := pod.Labels["undermoonService"]
	if !ok || serviceType != undermoonServiceTypeStorage {
		return framework.NewStatus(framework.Success, "")
	}

	podIndex, err := s.getPodIndex(pod.Name)
	if err != nil {
		klog.V(0).ErrorS(err, "failed to get pod index", "podName", pod.Name)
		errMsg := fmt.Sprintf("invalid pod name for undermoon Statefulset: %s", pod.Name)
		return framework.NewStatus(framework.Error, errMsg)
	}

	undermoonName, ok := pod.Labels["undermoonName"]
	if !ok {
		klog.V(0).ErrorS(err, "failed to get undermoon name", "podName", pod.Name)
		errMsg := fmt.Sprintf("label 'undermoonName' not found in pod: %s", pod.Name)
		return framework.NewStatus(framework.Error, errMsg)
	}

	klog.V(0).Infof("filter pod: %v", pod.Name)
	for _, existingPodInfo := range node.Pods {
		existingPod := existingPodInfo.Pod
		if pod.Namespace != existingPod.Namespace {
			continue
		}

		// Check if they are in the same undermoon cluster.
		umName, ok := existingPod.Labels["undermoonName"]
		if !ok || umName != undermoonName {
			continue
		}

		// Only check the storage nodes
		serviceType, ok := existingPod.Labels["undermoonService"]
		if !ok || serviceType != undermoonServiceTypeStorage {
			continue
		}

		podName := existingPod.Name
		index, err := s.getPodIndex(podName)
		if err != nil {
			continue
		}
		// In the same shard
		if podIndex/2 == index/2 {
			klog.V(1).Infof("same shard: namespace %s cluster %s pod %d %d",
				pod.Namespace, undermoonName, podIndex, index)
			return framework.NewStatus(framework.UnschedulableAndUnresolvable, "")
		}
	}
	return framework.NewStatus(framework.Success, "")
}

func (s *UndermoonTopology) getPodIndex(podName string) (int, error) {
	indexStr := podName[strings.LastIndex(podName, "-")+1:]
	index, err := strconv.Atoi(indexStr)
	if err != nil {
		return 0, err
	}
	return index, nil
}
