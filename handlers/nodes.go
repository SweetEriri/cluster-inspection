package handlers

import (
	"cluster-inspection/db"
	"cluster-inspection/utils"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// NodeInfo 结构体
type NodeInfo struct {
	Name                   string
	CPUCapacity            string
	MemoryCapacity         string
	AllocatableCPU         string
	AllocatableMemory      string
	CPUUsage               string
	CPUUsagePercent        float64
	MemoryUsage            string
	MemoryUsagePercent     float64
	DiskCapacity           string
	DiskUsage              string
	DiskUsagePercent       float64
	Load1                  float64
	Load5                  float64
	Load15                 float64
	NetworkReceive         string
	NetworkTransmit        string
	Conditions             []corev1.NodeCondition
	IsHealthy              bool
	IsCPUUsageCritical     bool
	IsMemoryUsageCritical  bool
	IsDiskUsageCritical    bool
	IsNetworkUsageCritical bool
}

// 获得节点资源信息
func GetNodeResourceInfo(clusterName string, promClient v1.API, config *utils.Config) ([]NodeInfo, error) {
	k8sClient, _, err := utils.GetKubernetesClient(clusterName, config)
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes client: %w", err)
	}

	// 获取所有节点的列表
	query := `count(node_uname_info) by (instance)`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, warnings, err := promClient.Query(ctx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to query Prometheus for node list: %w", err)
	}
	if len(warnings) > 0 {
		log.Printf("Warnings from Prometheus query: %v", warnings)
	}

	vector, ok := result.(model.Vector)
	if !ok {
		return nil, fmt.Errorf("unexpected result type from Prometheus query")
	}

	var (
		nodeInfos      []NodeInfo
		mu             sync.Mutex
		wg             sync.WaitGroup
		errs           []error
		processedCount int
		errorCount     int
	)

	log.Printf("Starting to process %d nodes", len(vector))

	for _, sample := range vector {
		instance := string(sample.Metric["instance"])
		wg.Add(1)
		go func(instance string) {
			defer wg.Done()
			nodeInfo, err := getNodeInfoFromPrometheus(promClient, instance)
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("error getting info for node %s: %v", instance, err))
				errorCount++
				mu.Unlock()
				log.Printf("Failed to get info for node %s: %v", instance, err)
				return
			}
			// log.Printf("Successfully got info for node %s", instance)

			// 获取节点的 Conditions
			conditions, err := getNodeConditions(k8sClient, nodeInfo.Name)
			if err != nil {
				log.Printf("Error getting conditions for node %s: %v", nodeInfo.Name, err)
			} else {
				nodeInfo.Conditions = conditions
			}

			mu.Lock()
			nodeInfos = append(nodeInfos, nodeInfo)
			processedCount++
			mu.Unlock()
		}(instance)
	}

	wg.Wait()

	log.Printf("Processed %d nodes, %d errors occurred, %d nodes info collected", len(vector), errorCount, processedCount)

	if processedCount != len(vector) {
		log.Printf("Warning: Not all nodes were processed successfully. Expected %d, got %d", len(vector), processedCount)
	}

	// 批量保存到数据库
	if err := saveNodeInfosToDB(nodeInfos, clusterName); err != nil {
		log.Printf("Error saving node infos to database: %v", err)
		errs = append(errs, err)
	} else {
		log.Printf("Successfully saved %d nodes to database", len(nodeInfos))
	}

	if len(errs) > 0 {
		return nodeInfos, fmt.Errorf("errors occurred while processing node info: %v", errs)
	}

	return nodeInfos, nil
}

func saveNodeInfosToDB(nodeInfos []NodeInfo, clusterName string) error {
	var dbNodeInfos []db.NodeInfoDB

	for _, nodeInfo := range nodeInfos {
		conditions, err := json.Marshal(nodeInfo.Conditions)
		if err != nil {
			log.Printf("Failed to marshal conditions for node %s: %v", nodeInfo.Name, err)
			continue
		}

		dbNodeInfo := db.NodeInfoDB{
			Cluster:                clusterName,
			Name:                   nodeInfo.Name,
			CPUCapacity:            nodeInfo.CPUCapacity,
			MemoryCapacity:         nodeInfo.MemoryCapacity,
			AllocatableCPU:         nodeInfo.AllocatableCPU,
			AllocatableMemory:      nodeInfo.AllocatableMemory,
			CPUUsage:               nodeInfo.CPUUsage,
			CPUUsagePercent:        nodeInfo.CPUUsagePercent,
			MemoryUsage:            nodeInfo.MemoryUsage,
			MemoryUsagePercent:     nodeInfo.MemoryUsagePercent,
			DiskCapacity:           nodeInfo.DiskCapacity,
			DiskUsage:              nodeInfo.DiskUsage,
			DiskUsagePercent:       nodeInfo.DiskUsagePercent,
			Load1:                  nodeInfo.Load1,
			Load5:                  nodeInfo.Load5,
			Load15:                 nodeInfo.Load15,
			NetworkReceive:         nodeInfo.NetworkReceive,
			NetworkTransmit:        nodeInfo.NetworkTransmit,
			Conditions:             string(conditions),
			IsHealthy:              nodeInfo.IsHealthy,
			IsCPUUsageCritical:     nodeInfo.IsCPUUsageCritical,
			IsMemoryUsageCritical:  nodeInfo.IsMemoryUsageCritical,
			IsDiskUsageCritical:    nodeInfo.IsDiskUsageCritical,
			IsNetworkUsageCritical: nodeInfo.IsNetworkUsageCritical,
			Timestamp:              time.Now(),
		}

		dbNodeInfos = append(dbNodeInfos, dbNodeInfo)
	}

	log.Printf("Attempting to save %d node infos to database", len(dbNodeInfos))

	// 使用重试机制
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		err := db.SaveNodeInfoBatch(dbNodeInfos)
		if err == nil {
			log.Printf("Successfully saved all %d node infos to database", len(dbNodeInfos))
			return nil
		}
		log.Printf("Attempt %d: Failed to save node infos to database: %v", i+1, err)
		time.Sleep(time.Second * time.Duration(i+1)) // 简单的退避策略
	}

	return fmt.Errorf("failed to save %d node infos to database after %d attempts", len(dbNodeInfos), maxRetries)
}

func getNodeInfoFromPrometheus(promClient v1.API, instance string) (NodeInfo, error) {
	nodeInfo := NodeInfo{
		Name: instance,
	}

	// 获取 CPU 容量
	query := fmt.Sprintf(`count(node_cpu_seconds_total{instance="%s", mode="idle"})`, instance)
	cpuCount, err := querySingleValue(promClient, query)
	if err != nil {
		log.Printf("Warning: failed to get CPU capacity for node %s: %v", instance, err)
		nodeInfo.CPUCapacity = "N/A"
	} else {
		nodeInfo.CPUCapacity = fmt.Sprintf("%v", cpuCount)
	}

	// 获取内存容量
	query = fmt.Sprintf(`node_memory_MemTotal_bytes{instance="%s"}`, instance)
	memoryTotal, err := querySingleValue(promClient, query)
	if err != nil {
		log.Printf("Warning: failed to get memory capacity for node %s: %v", instance, err)
		nodeInfo.MemoryCapacity = "N/A"
	} else {
		nodeInfo.MemoryCapacity = utils.FormatBytes(memoryTotal)
	}

	// 获取可分配 CPU
	query = fmt.Sprintf(`count(node_cpu_seconds_total{instance="%s", mode="idle"})`, instance)
	allocatableCPU, err := querySingleValue(promClient, query)
	if err != nil {
		log.Printf("Warning: failed to get allocatable CPU for node %s: %v", instance, err)
		nodeInfo.AllocatableCPU = "N/A"
	} else {
		nodeInfo.AllocatableCPU = fmt.Sprintf("%v", allocatableCPU)
	}

	// 获取可分配内存
	query = fmt.Sprintf(`node_memory_MemTotal_bytes{instance="%s"}`, instance)
	allocatableMemory, err := querySingleValue(promClient, query)
	if err != nil {
		log.Printf("Warning: failed to get allocatable memory for node %s: %v", instance, err)
		nodeInfo.AllocatableMemory = "N/A"
	} else {
		nodeInfo.AllocatableMemory = utils.FormatBytes(allocatableMemory)
	}

	// 获取 CPU 使用率
	query = fmt.Sprintf(`100 - (avg by(instance) (rate(node_cpu_seconds_total{instance="%s",mode="idle"}[5m])) * 100)`, instance)
	cpuUsage, err := querySingleValue(promClient, query)
	if err != nil {
		log.Printf("Warning: failed to get CPU usage for node %s: %v", instance, err)
		nodeInfo.CPUUsagePercent = -1
	} else {
		nodeInfo.CPUUsagePercent = cpuUsage
	}

	// 获取内存使用率
	query = fmt.Sprintf(`100 * (1 - ((node_memory_MemAvailable_bytes{instance="%s"} or node_memory_MemFree_bytes{instance="%s"}) / node_memory_MemTotal_bytes{instance="%s"}))`, instance, instance, instance)
	memoryUsage, err := querySingleValue(promClient, query)
	if err != nil {
		log.Printf("Warning: failed to get memory usage for node %s: %v", instance, err)
		nodeInfo.MemoryUsagePercent = -1
	} else {
		nodeInfo.MemoryUsagePercent = memoryUsage
	}

	// 获取 CPU 使用量
	cpuUsageBytes := cpuUsage * float64(allocatableCPU) / 100
	nodeInfo.CPUUsage = fmt.Sprintf("%.2f", cpuUsageBytes)

	// 获取内存使用量
	memoryUsageBytes := memoryTotal * memoryUsage / 100
	nodeInfo.MemoryUsage = utils.FormatBytes(memoryUsageBytes)

	// 获取磁盘容量
	query = fmt.Sprintf(`sum(node_filesystem_size_bytes{instance="%s", mountpoint="/"})`, instance)
	diskCapacity, err := querySingleValue(promClient, query)
	if err != nil {
		log.Printf("Warning: failed to get disk capacity for node %s: %v", instance, err)
		nodeInfo.DiskCapacity = "N/A"
	} else {
		nodeInfo.DiskCapacity = utils.FormatBytes(diskCapacity)
	}

	// 获取磁盘使用率
	query = fmt.Sprintf(`100 - ((sum(node_filesystem_free_bytes{instance="%s", mountpoint="/"}) * 100) / sum(node_filesystem_size_bytes{instance="%s", mountpoint="/"}) )`, instance, instance)
	diskUsage, err := querySingleValue(promClient, query)
	if err != nil {
		log.Printf("Warning: failed to get disk usage for node %s: %v", instance, err)
		nodeInfo.DiskUsagePercent = -1
	} else {
		nodeInfo.DiskUsagePercent = diskUsage
	}

	// 计算磁盘使用量
	diskUsageBytes := diskCapacity * diskUsage / 100
	nodeInfo.DiskUsage = utils.FormatBytes(diskUsageBytes)

	// 获取负载
	query = fmt.Sprintf(`node_load1{instance="%s"}`, instance)
	load1, err := querySingleValue(promClient, query)
	if err != nil {
		log.Printf("Warning: failed to get load1 for node %s: %v", instance, err)
	} else {
		nodeInfo.Load1 = load1
	}

	query = fmt.Sprintf(`node_load5{instance="%s"}`, instance)
	load5, err := querySingleValue(promClient, query)
	if err != nil {
		log.Printf("Warning: failed to get load5 for node %s: %v", instance, err)
	} else {
		nodeInfo.Load5 = load5
	}

	query = fmt.Sprintf(`node_load15{instance="%s"}`, instance)
	load15, err := querySingleValue(promClient, query)
	if err != nil {
		log.Printf("Warning: failed to get load15 for node %s: %v", instance, err)
	} else {
		nodeInfo.Load15 = load15
	}

	// 获取网络接收流量
	query = fmt.Sprintf(`sum(rate(node_network_receive_bytes_total{instance="%s"}[5m]))`, instance)
	networkReceive, err := querySingleValue(promClient, query)
	if err != nil {
		log.Printf("Warning: failed to get network receive for node %s: %v", instance, err)
		nodeInfo.NetworkReceive = "N/A"
	} else {
		nodeInfo.NetworkReceive = utils.FormatBytes(networkReceive)
	}

	// 获取网络发送流量
	query = fmt.Sprintf(`sum(rate(node_network_transmit_bytes_total{instance="%s"}[5m]))`, instance)
	networkTransmit, err := querySingleValue(promClient, query)
	if err != nil {
		log.Printf("Warning: failed to get network transmit for node %s: %v", instance, err)
		nodeInfo.NetworkTransmit = "N/A"
	} else {
		nodeInfo.NetworkTransmit = utils.FormatBytes(networkTransmit)
	}

	// 检查节点健康状态
	nodeInfo.IsHealthy = true
	nodeInfo.IsCPUUsageCritical = nodeInfo.CPUUsagePercent > 80
	nodeInfo.IsMemoryUsageCritical = nodeInfo.MemoryUsagePercent > 80
	nodeInfo.IsDiskUsageCritical = nodeInfo.DiskUsagePercent > 80

	if nodeInfo.IsCPUUsageCritical || nodeInfo.IsMemoryUsageCritical || nodeInfo.IsDiskUsageCritical || nodeInfo.IsNetworkUsageCritical {
		nodeInfo.IsHealthy = false
	}

	return nodeInfo, nil
}

func querySingleValue(promClient v1.API, query string) (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, warnings, err := promClient.Query(ctx, query, time.Now())
	if err != nil {
		return 0, fmt.Errorf("error querying Prometheus: %w", err)
	}
	if len(warnings) > 0 {
		log.Printf("Warnings from Prometheus query: %v", warnings)
	}

	switch v := result.(type) {
	case model.Vector:
		if len(v) == 0 {
			return 0, fmt.Errorf("no data returned from query: %s", query)
		}
		return float64(v[0].Value), nil
	case *model.Scalar:
		return float64(v.Value), nil
	default:
		return 0, fmt.Errorf("unexpected result type from query: %s", query)
	}
}

// 获取节点的 Conditions
func getNodeConditions(client *kubernetes.Clientset, instanceName string) ([]corev1.NodeCondition, error) {
	// 移除端口号
	instanceIP := strings.Split(instanceName, ":")[0]

	// 获取所有节点
	nodes, err := client.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	// 查找匹配的节点
	for _, node := range nodes.Items {
		if nodeMatchesInstance(node, instanceIP) {
			// log.Printf("Matched instance %s to Kubernetes node %s", instanceName, node.Name)
			return node.Status.Conditions, nil
		}
	}

	return nil, fmt.Errorf("node not found for instance: %s", instanceName)
}

func nodeMatchesInstance(node corev1.Node, instanceIP string) bool {
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeInternalIP && addr.Address == instanceIP {
			return true
		}
	}
	return false
}

func GetNodeInfoFromDB(w http.ResponseWriter, r *http.Request) {
	query, err := utils.ParseTimeRangeQuery(r)
	if err != nil {
		http.Error(w, "Invalid query parameters", http.StatusBadRequest)
		return
	}

	// 获取集群名称参数
	clusterName := r.URL.Query().Get("cluster")

	var nodeInfos []db.NodeInfoDB
	if query.UseTimeRange {
		nodeInfos, err = db.GetNodeInfo(clusterName, 0, 0, query.StartTime, query.EndTime)
	} else {
		nodeInfos, err = db.GetNodeInfo(clusterName, query.Limit, query.Offset, time.Time{}, time.Time{})
	}

	if err != nil {
		http.Error(w, "Error retrieving node info from database", http.StatusInternalServerError)
		log.Printf("Error retrieving node info: %v", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(nodeInfos); err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
		log.Printf("Error encoding response: %v", err)
	}
}
