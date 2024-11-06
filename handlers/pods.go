package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"cluster-inspection/db"
	"cluster-inspection/utils"

	"strconv"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// PodInfo 结构体
type PodInfo struct {
	Name                   string
	Namespace              string
	Labels                 map[string]string
	CPUUsageMax            string
	CPUUsageMin            string
	CPUUsageAvg            string
	MemoryUsageMax         string
	MemoryUsageMin         string
	MemoryUsageAvg         string
	CPULimit               string
	MemoryLimit            string
	CPUUsagePercent        float64
	MemoryUsagePercent     float64
	CPUUsagePercentMax     float64
	CPUUsagePercentMin     float64
	CPUUsagePercentAvg     float64
	MemoryUsagePercentMax  float64
	MemoryUsagePercentMin  float64
	MemoryUsagePercentAvg  float64
	Status                 string
	RestartCount           int
	IsCPUUsageCritical     bool
	IsMemoryUsageCritical  bool
	IsRestartCountCritical bool
	ContainerStatuses      []ContainerStatusInfo
	Category               string
	OwnerName              string
	CPUUsage               float64
	MemoryUsage            float64
	CPURequest             string
	MemoryRequest          string
	PodIP                  string
	Timestamp              time.Time
}

// ContainerStatusInfo 结构体，用于保存每个容器的状态信息
type ContainerStatusInfo struct {
	Name                  string
	State                 string
	Reason                string
	Message               string
	CPUUsageMax           string
	CPUUsageMin           string
	CPUUsageAvg           string
	MemoryUsageMax        string
	MemoryUsageMin        string
	MemoryUsageAvg        string
	MemoryLimit           string
	CPULimit              string
	CPUUsagePercentMax    float64
	CPUUsagePercentMin    float64
	CPUUsagePercentAvg    float64
	MemoryUsagePercentMax float64
	MemoryUsagePercentMin float64
	MemoryUsagePercentAvg float64
	CPURequest            string
	MemoryRequest         string
}

// 优化 UsageStats 结构
type UsageStats struct {
	Max float64
	Min float64
	Avg float64
}

const restartCountThreshold = 3

// DeploymentResourceRecommendation 结构体用存储 Deployment 级别的资源调整建议
type DeploymentResourceRecommendation struct {
	DeploymentName          string
	Namespace               string
	Replicas                int
	AverageCPUUsage         float64
	AverageMemoryUsage      float64
	CPULimit                float64
	MemoryLimit             float64
	CPURequest              float64
	MemoryRequest           float64
	CPURequestAdjustment    string
	CPULimitAdjustment      string
	MemoryRequestAdjustment string
	MemoryLimitAdjustment   string
	LatestPodsByCategory    map[string][]PodInfo
	MaxCPUUsage             float64
	MaxMemoryUsage          float64
}

// PodResourceRecommendation 结构体用于存储单个 Pod 的资源使用情况
type PodResourceRecommendation struct {
	PodName            string
	CPUUsage           string
	MemoryUsage        string
	CPULimit           string
	MemoryLimit        string
	CPUUsagePercent    float64
	MemoryUsagePercent float64
	CPURequest         string
	MemoryRequest      string
	Containers         []ContainerResourceRecommendation
}

// Add a new struct for container-specific information
type ContainerResourceRecommendation struct {
	Name               string
	CPUUsage           string
	MemoryUsage        string
	CPULimit           string
	MemoryLimit        string
	CPUUsagePercent    float64
	MemoryUsagePercent float64
	CPURequest         string
	MemoryRequest      string
}

type NamespaceRecommendation struct {
	CPURequestSuggestion    string
	CPULimitSuggestion      string
	MemoryRequestSuggestion string
	MemoryLimitSuggestion   string
}
type ContainerResources struct {
	CPULimit       float64
	MemoryLimit    float64
	CPURequest     float64
	MemoryRequest  float64
	CPUUsageAvg    string
	MemoryUsageAvg string
	CPUUsageMax    string
	MemoryUsageMax string
}

func GetPodInfoFromDB(w http.ResponseWriter, r *http.Request) {
	query, err := utils.ParseTimeRangeQuery(r)
	if err != nil {
		http.Error(w, "Invalid query parameters", http.StatusBadRequest)
		return
	}

	clusterName := r.URL.Query().Get("cluster")
	podName := r.URL.Query().Get("pod_name")

	var podInfos []db.PodInfoDB
	if query.UseTimeRange {
		podInfos, err = db.GetPodInfo(clusterName, podName, 0, 0, query.StartTime, query.EndTime)
	} else {
		podInfos, err = db.GetPodInfo(clusterName, podName, query.Limit, query.Offset, time.Time{}, time.Time{})
	}

	if err != nil {
		http.Error(w, "Error retrieving pod info from database", http.StatusInternalServerError)
		log.Printf("Error retrieving pod info: %v", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(podInfos); err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
		log.Printf("Error encoding response: %v", err)
	}
}

func GetPodResourceInfo(clusterName string, promClient v1.API, config *utils.Config, page, pageSize int) ([]PodInfo, int, error) {
	if pageSize < 0 {
		return nil, 0, fmt.Errorf("invalid page size: %d", pageSize)
	}
	if pageSize > 100 {
		pageSize = 100
	}
	return fetchPodResourceInfo(clusterName, promClient, config, page, pageSize)
}

func fetchPodResourceInfo(clusterName string, promClient v1.API, config *utils.Config, page, pageSize int) ([]PodInfo, int, error) {
	clientset, _, err := utils.GetKubernetesClient(clusterName, config)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get Kubernetes client: %w", err)
	}

	// 首先获取所有 Pod 的总数
	allPods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list all pods: %w", err)
	}
	totalPods := len(allPods.Items)

	// 然后根据分页参数获取当前页的 Pod
	listOptions := metav1.ListOptions{
		Limit:    int64(pageSize),
		Continue: "",
	}
	if page > 1 && pageSize > 0 {
		listOptions.Continue = allPods.Continue
		for i := 1; i < page; i++ {
			tempPods, err := clientset.CoreV1().Pods("").List(context.TODO(), listOptions)
			if err != nil {
				return nil, 0, fmt.Errorf("failed to list pods for page %d: %w", i, err)
			}
			listOptions.Continue = tempPods.Continue
			if listOptions.Continue == "" {
				break
			}
		}
	}

	pods, err := clientset.CoreV1().Pods("").List(context.TODO(), listOptions)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list pods: %w", err)
	}

	paginatedPods := pods.Items
	if pageSize == 0 {
		paginatedPods = allPods.Items
	}

	startTime, endTime := getPreviousDayTimeRange()
	podNames := make([]string, len(paginatedPods))
	for i, pod := range paginatedPods {
		podNames[i] = pod.Name
	}

	cpuUsageMap, memoryUsageMap, err := getUsageStatsForPods(promClient, podNames, startTime, endTime)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get usage stats: %w", err)
	}

	podInfos := ConvertPodListToPodInfo(paginatedPods, clientset, cpuUsageMap, memoryUsageMap)

	// 保存 Pod 信息到数据库
	if err := SavePodInfoToDB(podInfos, clusterName); err != nil {
		log.Printf("Error saving pod info to database: %v", err)
	}

	return podInfos, totalPods, nil
}

func getUsageStatsForPods(promClient v1.API, podNames []string, startTime, endTime time.Time) (map[string]map[string]UsageStats, map[string]map[string]UsageStats, error) {
	cpuUsageMap := make(map[string]map[string]UsageStats)
	memoryUsageMap := make(map[string]map[string]UsageStats)
	var wg sync.WaitGroup
	errChan := make(chan error, 6)

	wg.Add(6)
	for _, aggregation := range []string{"max", "min", "avg"} {
		go func(agg string) {
			defer wg.Done()
			result, err := getUsageStatsWithRetry(promClient, buildCPUQuery(podNames, agg), startTime, endTime)
			if err != nil {
				errChan <- fmt.Errorf("failed to get CPU %s usage: %w", agg, err)
				return
			}
			mergeUsageStats(cpuUsageMap, result, agg)
		}(aggregation)

		go func(agg string) {
			defer wg.Done()
			result, err := getUsageStatsWithRetry(promClient, buildMemoryQuery(podNames, agg), startTime, endTime)
			if err != nil {
				errChan <- fmt.Errorf("failed to get memory %s usage: %w", agg, err)
				return
			}
			mergeUsageStats(memoryUsageMap, result, agg)
		}(aggregation)
	}

	wg.Wait()
	close(errChan)

	if err := <-errChan; err != nil {
		return nil, nil, err
	}

	return cpuUsageMap, memoryUsageMap, nil
}

func getUsageStatsWithRetry(promClient v1.API, query string, startTime, endTime time.Time) (map[string]map[string]UsageStats, error) {
	var result map[string]map[string]UsageStats
	var err error
	maxRetries := 3
	retryDelay := time.Second

	for i := 0; i < maxRetries; i++ {
		result, err = getUsageStats(promClient, query, startTime, endTime)
		if err == nil {
			return result, nil
		}

		if i < maxRetries-1 {
			log.Printf("Retry %d: Failed to query Prometheus: %v. Retrying in %v...", i+1, err, retryDelay)
			time.Sleep(retryDelay)
			retryDelay *= 2 // 指数退避
		}
	}

	return nil, fmt.Errorf("failed to query Prometheus after %d attempts: %w", maxRetries, err)
}

func getUsageStats(promClient v1.API, query string, startTime, endTime time.Time) (map[string]map[string]UsageStats, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, _, err := promClient.QueryRange(ctx, query, v1.Range{
		Start: startTime,
		End:   endTime,
		Step:  time.Minute * 5,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to query Prometheus: %w", err)
	}

	usageMap := make(map[string]map[string]UsageStats)
	if matrix, ok := result.(model.Matrix); ok {
		for _, sampleStream := range matrix {
			pod := string(sampleStream.Metric["pod"])
			container := string(sampleStream.Metric["container"])

			if _, ok := usageMap[pod]; !ok {
				usageMap[pod] = make(map[string]UsageStats)
			}

			var max, min, sum float64
			count := 0
			for _, sample := range sampleStream.Values {
				value := float64(sample.Value)
				if count == 0 || value > max {
					max = value
				}
				if count == 0 || value < min {
					min = value
				}
				sum += value
				count++
			}

			avg := 0.0
			if count > 0 {
				avg = sum / float64(count)
			}

			usageMap[pod][container] = UsageStats{
				Max: max,
				Min: min,
				Avg: avg,
			}
		}
	}

	return usageMap, nil
}

func buildCPUQuery(podNames []string, aggregation string) string {
	podSelector := strings.Join(podNames, "|")
	return fmt.Sprintf(`%s(rate(container_cpu_usage_seconds_total{pod=~"%s"}[5m])) by (pod, container)`, aggregation, podSelector)
}

func buildMemoryQuery(podNames []string, aggregation string) string {
	podSelector := strings.Join(podNames, "|")
	return fmt.Sprintf(`%s(container_memory_working_set_bytes{pod=~"%s"}) by (pod, container)`, aggregation, podSelector)
}

func mergeUsageStats(target map[string]map[string]UsageStats, source map[string]map[string]UsageStats, aggregation string) {
	for pod, containers := range source {
		if _, exists := target[pod]; !exists {
			target[pod] = make(map[string]UsageStats)
		}
		for container, stats := range containers {
			currentStats := target[pod][container]
			switch aggregation {
			case "max":
				currentStats.Max = stats.Max
			case "min":
				currentStats.Min = stats.Min
			case "avg":
				currentStats.Avg = stats.Avg
			}
			target[pod][container] = currentStats
		}
	}
}

func ConvertPodListToPodInfo(pods []corev1.Pod, clientset *kubernetes.Clientset, cpuUsageMap, memoryUsageMap map[string]map[string]UsageStats) []PodInfo {
	var podInfos []PodInfo
	var mu sync.Mutex
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 20) // 限制并发数

	for i := range pods {
		wg.Add(1)
		semaphore <- struct{}{}
		go func(pod corev1.Pod) {
			defer wg.Done()
			defer func() { <-semaphore }()
			podInfo := convertSinglePodToPodInfo(&pod, clientset, cpuUsageMap, memoryUsageMap)
			mu.Lock()
			podInfos = append(podInfos, podInfo)
			mu.Unlock()
		}(pods[i])
	}

	wg.Wait()
	return podInfos
}

func convertSinglePodToPodInfo(pod *corev1.Pod, clientset *kubernetes.Clientset, cpuUsageMap, memoryUsageMap map[string]map[string]UsageStats) PodInfo {
	var appCPULimit, appMemoryLimit float64
	var appCPURequest, appMemoryRequest float64
	var totalRestartCount int
	var hasCPULimit, hasMemoryLimit, hasCPURequest, hasMemoryRequest bool

	// 计算 Pod 中每个容器的资源限制和请求
	for _, container := range pod.Spec.Containers {
		if container.Resources.Limits.Cpu() != nil {
			appCPULimit += float64(container.Resources.Limits.Cpu().MilliValue()) / 1000
			hasCPULimit = true
		}
		if container.Resources.Limits.Memory() != nil {
			appMemoryLimit += float64(container.Resources.Limits.Memory().Value()) / (1024 * 1024) // 转换为 MiB
			hasMemoryLimit = true
		}
		if container.Resources.Requests.Cpu() != nil {
			appCPURequest += float64(container.Resources.Requests.Cpu().MilliValue()) / 1000
			hasCPURequest = true
		}
		if container.Resources.Requests.Memory() != nil {
			appMemoryRequest += float64(container.Resources.Requests.Memory().Value()) / (1024 * 1024) // 转换为 MiB
			hasMemoryRequest = true
		}
	}

	cpuUsage := cpuUsageMap[pod.Name]
	memoryUsage := memoryUsageMap[pod.Name]

	var containerStatuses []ContainerStatusInfo
	var totalCPUMax, totalCPUMin, totalCPUAvg float64
	var totalMemoryMax, totalMemoryMin, totalMemoryAvg float64

	podAge := time.Since(pod.CreationTimestamp.Time)
	for _, container := range pod.Spec.Containers {
		containerCPULimit := float64(container.Resources.Limits.Cpu().MilliValue()) / 1000
		containerMemoryLimit := float64(container.Resources.Limits.Memory().Value()) / (1024 * 1024)

		containerCPULimitStr := "No Limit"
		containerMemoryLimitStr := "No Limit"
		if containerCPULimit > 0 {
			containerCPULimitStr = fmt.Sprintf("%.2f cores", containerCPULimit)
		}
		if containerMemoryLimit > 0 {
			containerMemoryLimitStr = fmt.Sprintf("%.2f MiB", containerMemoryLimit)
		}

		containerCPUStats := cpuUsage[container.Name]
		containerMemoryStats := memoryUsage[container.Name]

		if podAge < time.Hour {
			// 对于运行时间不足一小时的 Pod，使用当前值作为最大值、最小值和平均值
			containerCPUStats.Min = containerCPUStats.Max
			containerCPUStats.Avg = containerCPUStats.Max
			containerMemoryStats.Min = containerMemoryStats.Max
			containerMemoryStats.Avg = containerMemoryStats.Max
		}

		// 获取容器的状态
		var containerState, containerReason, containerMessage string
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == container.Name {
				if status.State.Waiting != nil {
					containerState = "Waiting"
					containerReason = status.State.Waiting.Reason
					containerMessage = status.State.Waiting.Message
				} else if status.State.Running != nil {
					containerState = "Running"
				} else if status.State.Terminated != nil {
					containerState = "Terminated"
					containerReason = status.State.Terminated.Reason
					containerMessage = status.State.Terminated.Message
				}
				// 累加重启次数
				totalRestartCount += int(status.RestartCount)
				break
			}
		}

		// 加容器的 CPU 和内存使用情况
		totalCPUMax += containerCPUStats.Max
		totalCPUMin += containerCPUStats.Min
		totalCPUAvg += containerCPUStats.Avg
		totalMemoryMax += containerMemoryStats.Max / (1024 * 1024) // 转换为 MiB
		totalMemoryMin += containerMemoryStats.Min / (1024 * 1024)
		totalMemoryAvg += containerMemoryStats.Avg / (1024 * 1024)

		// 构建容器状态信息
		statusInfo := ContainerStatusInfo{
			Name:                  container.Name,
			State:                 containerState,
			Reason:                containerReason,
			Message:               containerMessage,
			CPULimit:              containerCPULimitStr,
			MemoryLimit:           containerMemoryLimitStr,
			CPUUsageMax:           fmt.Sprintf("%.2f cores", utils.SanitizeFloatValue(containerCPUStats.Max)),
			CPUUsageMin:           fmt.Sprintf("%.2f cores", utils.SanitizeFloatValue(containerCPUStats.Min)),
			CPUUsageAvg:           fmt.Sprintf("%.2f cores", utils.SanitizeFloatValue(containerCPUStats.Avg)),
			MemoryUsageMax:        fmt.Sprintf("%.2f MiB", utils.SanitizeFloatValue(containerMemoryStats.Max/(1024*1024))),
			MemoryUsageMin:        fmt.Sprintf("%.2f MiB", utils.SanitizeFloatValue(containerMemoryStats.Min/(1024*1024))),
			MemoryUsageAvg:        fmt.Sprintf("%.2f MiB", utils.SanitizeFloatValue(containerMemoryStats.Avg/(1024*1024))),
			CPUUsagePercentMax:    utils.SanitizeFloatValue(containerCPUStats.Max / containerCPULimit * 100),
			CPUUsagePercentMin:    utils.SanitizeFloatValue(containerCPUStats.Min / containerCPULimit * 100),
			CPUUsagePercentAvg:    utils.SanitizeFloatValue(containerCPUStats.Avg / containerCPULimit * 100),
			MemoryUsagePercentMax: utils.SanitizeFloatValue(containerMemoryStats.Max / (containerMemoryLimit * 1024 * 1024) * 100),
			MemoryUsagePercentMin: utils.SanitizeFloatValue(containerMemoryStats.Min / (containerMemoryLimit * 1024 * 1024) * 100),
			MemoryUsagePercentAvg: utils.SanitizeFloatValue(containerMemoryStats.Avg / (containerMemoryLimit * 1024 * 1024) * 100),
			CPURequest:            container.Resources.Requests.Cpu().String(),
			MemoryRequest:         container.Resources.Requests.Memory().String(),
		}

		containerStatuses = append(containerStatuses, statusInfo)
	}

	// 判断重启次数是否超过阈值
	isRestartCountCritical := totalRestartCount > restartCountThreshold

	// 判断 CPU 和内存用是否临界
	isCPUUsageCritical := totalCPUAvg/appCPULimit > 0.8
	isMemoryUsageCritical := totalMemoryAvg/appMemoryLimit > 0.8

	category, ownerName := categorizePod(clientset, pod)

	cpuUsagePercent := calculateUsagePercent(totalCPUAvg, appCPULimit)
	memoryUsagePercent := calculateUsagePercent(totalMemoryAvg, appMemoryLimit)
	cpuUsagePercentMax := calculateUsagePercent(totalCPUMax, appCPULimit)
	cpuUsagePercentMin := calculateUsagePercent(totalCPUMin, appCPULimit)
	cpuUsagePercentAvg := calculateUsagePercent(totalCPUAvg, appCPULimit)
	memoryUsagePercentMax := calculateUsagePercent(totalMemoryMax, appMemoryLimit)
	memoryUsagePercentMin := calculateUsagePercent(totalMemoryMin, appMemoryLimit)
	memoryUsagePercentAvg := calculateUsagePercent(totalMemoryAvg, appMemoryLimit)

	// 构建 PodInfo 对象
	return PodInfo{
		Name:                   pod.Name,
		Namespace:              pod.Namespace,
		Labels:                 pod.Labels,
		CPUUsageMax:            fmt.Sprintf("%.2f cores", utils.SanitizeFloatValue(totalCPUMax)),
		CPUUsageMin:            fmt.Sprintf("%.2f cores", utils.SanitizeFloatValue(totalCPUMin)),
		CPUUsageAvg:            fmt.Sprintf("%.2f cores", utils.SanitizeFloatValue(totalCPUAvg)),
		MemoryUsageMax:         fmt.Sprintf("%.2f MiB", utils.SanitizeFloatValue(totalMemoryMax)),
		MemoryUsageMin:         fmt.Sprintf("%.2f MiB", utils.SanitizeFloatValue(totalMemoryMin)),
		MemoryUsageAvg:         fmt.Sprintf("%.2f MiB", utils.SanitizeFloatValue(totalMemoryAvg)),
		CPURequest:             formatResourceValue(appCPURequest, "cores", hasCPURequest),
		MemoryRequest:          formatResourceValue(appMemoryRequest, "MiB", hasMemoryRequest),
		CPULimit:               formatResourceValue(appCPULimit, "cores", hasCPULimit),
		MemoryLimit:            formatResourceValue(appMemoryLimit, "MiB", hasMemoryLimit),
		CPUUsagePercent:        cpuUsagePercent,
		MemoryUsagePercent:     memoryUsagePercent,
		CPUUsagePercentMax:     cpuUsagePercentMax,
		CPUUsagePercentMin:     cpuUsagePercentMin,
		CPUUsagePercentAvg:     cpuUsagePercentAvg,
		MemoryUsagePercentMax:  memoryUsagePercentMax,
		MemoryUsagePercentMin:  memoryUsagePercentMin,
		MemoryUsagePercentAvg:  memoryUsagePercentAvg,
		Status:                 determinePodStatus(containerStatuses),
		RestartCount:           totalRestartCount,
		IsCPUUsageCritical:     isCPUUsageCritical,
		IsMemoryUsageCritical:  isMemoryUsageCritical,
		IsRestartCountCritical: isRestartCountCritical,
		ContainerStatuses:      containerStatuses,
		Category:               category,
		OwnerName:              ownerName,
		CPUUsage:               totalCPUAvg,
		MemoryUsage:            totalMemoryAvg,
		PodIP:                  pod.Status.PodIP,
		Timestamp:              time.Now(),
	}
}

// determinePodStatus 根据容器状态推断 Pod 状态
func determinePodStatus(containerStatuses []ContainerStatusInfo) string {
	for _, status := range containerStatuses {
		if status.State == "Waiting" {
			return "Waiting"
		}
		if status.State == "Terminated" {
			return "Terminated"
		}
	}
	return "Running"
}

func categorizePod(clientset *kubernetes.Clientset, pod *corev1.Pod) (string, string) {
	for _, ownerRef := range pod.OwnerReferences {
		switch ownerRef.Kind {
		case "ReplicaSet":
			rs, err := clientset.AppsV1().ReplicaSets(pod.Namespace).Get(context.TODO(), ownerRef.Name, metav1.GetOptions{})
			if err == nil {
				for _, rsOwnerRef := range rs.OwnerReferences {
					if rsOwnerRef.Kind == "Deployment" {
						return "Deployment", rsOwnerRef.Name
					}
				}
			}
			return "ReplicaSet", ownerRef.Name
		case "StatefulSet":
			return "StatefulSet", ownerRef.Name
		case "DaemonSet":
			return "DaemonSet", ownerRef.Name
		case "Job":
			job, err := clientset.BatchV1().Jobs(pod.Namespace).Get(context.TODO(), ownerRef.Name, metav1.GetOptions{})
			if err == nil {
				for _, jobOwnerRef := range job.OwnerReferences {
					if jobOwnerRef.Kind == "CronJob" {
						return "CronJob", jobOwnerRef.Name
					}
				}
			}
			return "Job", ownerRef.Name
		}
	}
	return "Standalone", ""
}

func getPreviousDayTimeRange() (time.Time, time.Time) {
	now := time.Now()
	startTime := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	endTime := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 999999999, now.Location())
	return startTime, endTime
}

// 保存 Pod 信息到数据库
func SavePodInfoToDB(podInfos []PodInfo, clusterName string) error {
	var podInfoDBs []db.PodInfoDB
	for _, podInfo := range podInfos {
		podInfoDB := db.PodInfoDB{
			Cluster:                clusterName,
			Name:                   podInfo.Name,
			Namespace:              podInfo.Namespace,
			Labels:                 utils.MapToString(podInfo.Labels),
			CPUUsageMax:            podInfo.CPUUsageMax,
			CPUUsageMin:            podInfo.CPUUsageMin,
			CPUUsageAvg:            podInfo.CPUUsageAvg,
			MemoryUsageMax:         podInfo.MemoryUsageMax,
			MemoryUsageMin:         podInfo.MemoryUsageMin,
			MemoryUsageAvg:         podInfo.MemoryUsageAvg,
			CPULimit:               podInfo.CPULimit,
			MemoryLimit:            podInfo.MemoryLimit,
			CPUUsagePercent:        db.NullFloat64{NullFloat64: sql.NullFloat64{Float64: podInfo.CPUUsagePercent, Valid: true}},
			MemoryUsagePercent:     db.NullFloat64{NullFloat64: sql.NullFloat64{Float64: podInfo.MemoryUsagePercent, Valid: true}},
			CPUUsagePercentMax:     db.NullFloat64{NullFloat64: sql.NullFloat64{Float64: podInfo.CPUUsagePercentMax, Valid: true}},
			CPUUsagePercentMin:     db.NullFloat64{NullFloat64: sql.NullFloat64{Float64: podInfo.CPUUsagePercentMin, Valid: true}},
			CPUUsagePercentAvg:     db.NullFloat64{NullFloat64: sql.NullFloat64{Float64: podInfo.CPUUsagePercentAvg, Valid: true}},
			MemoryUsagePercentMax:  db.NullFloat64{NullFloat64: sql.NullFloat64{Float64: podInfo.MemoryUsagePercentMax, Valid: true}},
			MemoryUsagePercentMin:  db.NullFloat64{NullFloat64: sql.NullFloat64{Float64: podInfo.MemoryUsagePercentMin, Valid: true}},
			MemoryUsagePercentAvg:  db.NullFloat64{NullFloat64: sql.NullFloat64{Float64: podInfo.MemoryUsagePercentAvg, Valid: true}},
			Status:                 podInfo.Status,
			RestartCount:           podInfo.RestartCount,
			IsCPUUsageCritical:     podInfo.IsCPUUsageCritical,
			IsMemoryUsageCritical:  podInfo.IsMemoryUsageCritical,
			IsRestartCountCritical: podInfo.IsRestartCountCritical,
			ContainerStatuses:      utils.StructToString(podInfo.ContainerStatuses),
			Category:               podInfo.Category,
			OwnerName:              podInfo.OwnerName,
			Timestamp:              podInfo.Timestamp,
			CPURequest:             podInfo.CPURequest,
			MemoryRequest:          podInfo.MemoryRequest,
			PodIP:                  podInfo.PodIP,
		}
		podInfoDBs = append(podInfoDBs, podInfoDB)
	}

	return db.SavePodInfoBatch(podInfoDBs)
}

func calculateUsagePercent(usage, limit float64) float64 {
	if limit > 0 {
		return utils.SanitizeFloatValue(usage / limit * 100)
	}
	return -1 // 表示无限制
}

func formatResourceValue(value float64, unit string, isSet bool) string {
	if !isSet {
		return "Not Set"
	}
	return fmt.Sprintf("%.2f %s", value, unit)
}

func GetDeploymentResourceRecommendations(w http.ResponseWriter, r *http.Request) {
	// 解析查询参数
	clusterName := r.URL.Query().Get("cluster")
	if clusterName == "" {
		http.Error(w, "Cluster name is required", http.StatusBadRequest)
		return
	}

	// 从数据库获取过去7天的 Pod 信息
	podInfos, err := getPodInfoFromDB(clusterName, 7)
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting pod info from database: %v", err), http.StatusInternalServerError)
		return
	}

	recommendations := generateDeploymentRecommendations(podInfos)

	// 获取每个 Deployment 最新的三个 Pod
	for i, rec := range recommendations {
		latestPods, err := getLatestPodsForOwner(clusterName, rec.Namespace, rec.DeploymentName, 3)
		if err != nil {
			log.Printf("Error getting latest pods for owner %s: %v", rec.DeploymentName, err)
			continue
		}
		recommendations[i].LatestPodsByCategory = make(map[string][]PodInfo)
		for category, pods := range latestPods {
			recommendations[i].LatestPodsByCategory[category] = convertPodInfoDBsToPodInfos(pods)
		}
	}

	// 构建响应
	response := struct {
		Recommendations []DeploymentResourceRecommendation `json:"recommendations"`
	}{
		Recommendations: recommendations,
	}

	// 返回 JSON 响应
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("Error encoding response: %v", err)
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
		return
	}

	log.Printf("Successfully sent %d recommendations", len(recommendations))
}

func convertPodInfoDBsToPodInfos(podInfoDBs []db.PodInfoDB) []PodInfo {
	var podInfos []PodInfo
	for _, podInfoDB := range podInfoDBs {
		podInfo := convertPodInfoDBToPodInfo(podInfoDB)
		podInfos = append(podInfos, podInfo)
	}
	return podInfos
}

func getLatestPodsForOwner(clusterName, namespace, ownerName string, limit int) (map[string][]db.PodInfoDB, error) {
	var pods []db.PodInfoDB
	err := db.DB.Where("cluster = ? AND namespace = ? AND owner_name = ?",
		clusterName, namespace, ownerName).
		Order("timestamp desc").
		Limit(limit).
		Find(&pods).Error

	if err != nil {
		return nil, fmt.Errorf("error querying pod info: %w", err)
	}

	// 按 category 分组
	podsByCategory := make(map[string][]db.PodInfoDB)
	for _, pod := range pods {
		podsByCategory[pod.Category] = append(podsByCategory[pod.Category], pod)
	}

	return podsByCategory, nil
}

func getPodInfoFromDB(clusterName string, days int) ([]PodInfo, error) {
	endTime := time.Now()
	startTime := endTime.AddDate(0, 0, -days)

	podInfoDBs, err := db.GetPodInfo(clusterName, "", 0, 0, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("error retrieving pod info from database: %w", err)
	}

	var podInfos []PodInfo
	for _, podInfoDB := range podInfoDBs {
		podInfo := convertPodInfoDBToPodInfo(podInfoDB)
		podInfos = append(podInfos, podInfo)
	}

	// 打印诊断信息
	log.Printf("Retrieved %d pod infos from database", len(podInfos))
	if len(podInfos) > 0 {
		log.Printf("Sample pod info: %+v", podInfos[0])
	}

	return podInfos, nil
}

func convertPodInfoDBToPodInfo(podInfoDB db.PodInfoDB) PodInfo {
	return PodInfo{
		Name:                  podInfoDB.Name,
		Namespace:             podInfoDB.Namespace,
		CPUUsageMax:           podInfoDB.CPUUsageMax,
		CPUUsageMin:           podInfoDB.CPUUsageMin,
		CPUUsageAvg:           podInfoDB.CPUUsageAvg,
		MemoryUsageMax:        podInfoDB.MemoryUsageMax,
		MemoryUsageMin:        podInfoDB.MemoryUsageMin,
		MemoryUsageAvg:        podInfoDB.MemoryUsageAvg,
		CPURequest:            podInfoDB.CPURequest,
		MemoryRequest:         podInfoDB.MemoryRequest,
		CPULimit:              podInfoDB.CPULimit,
		MemoryLimit:           podInfoDB.MemoryLimit,
		CPUUsagePercent:       utils.SanitizeFloatValue(podInfoDB.CPUUsagePercent.Float64),
		MemoryUsagePercent:    utils.SanitizeFloatValue(podInfoDB.MemoryUsagePercent.Float64),
		CPUUsagePercentMax:    utils.SanitizeFloatValue(podInfoDB.CPUUsagePercentMax.Float64),
		CPUUsagePercentMin:    utils.SanitizeFloatValue(podInfoDB.CPUUsagePercentMin.Float64),
		CPUUsagePercentAvg:    utils.SanitizeFloatValue(podInfoDB.CPUUsagePercentAvg.Float64),
		MemoryUsagePercentMax: utils.SanitizeFloatValue(podInfoDB.MemoryUsagePercentMax.Float64),
		MemoryUsagePercentMin: utils.SanitizeFloatValue(podInfoDB.MemoryUsagePercentMin.Float64),
		MemoryUsagePercentAvg: utils.SanitizeFloatValue(podInfoDB.MemoryUsagePercentAvg.Float64),
		Status:                podInfoDB.Status,
		RestartCount:          podInfoDB.RestartCount,
		Category:              podInfoDB.Category,
		OwnerName:             podInfoDB.OwnerName,
		Timestamp:             podInfoDB.Timestamp,
	}
}

func generateDeploymentRecommendations(podInfos []PodInfo) []DeploymentResourceRecommendation {
	deploymentMap := make(map[string]*DeploymentResourceRecommendation)

	for _, pod := range podInfos {
		key := fmt.Sprintf("%s/%s", pod.Namespace, pod.OwnerName)
		if _, exists := deploymentMap[key]; !exists {
			deploymentMap[key] = &DeploymentResourceRecommendation{
				DeploymentName: pod.OwnerName,
				Namespace:      pod.Namespace,
				Replicas:       1,
			}
		} else {
			deploymentMap[key].Replicas++
		}

		// 处理主容器的资源设置
		mainContainerResources := getMainContainerResources(pod)
		if deploymentMap[key].CPULimit == 0 {
			deploymentMap[key].CPULimit = mainContainerResources.CPULimit
			deploymentMap[key].MemoryLimit = mainContainerResources.MemoryLimit
			deploymentMap[key].CPURequest = mainContainerResources.CPURequest
			deploymentMap[key].MemoryRequest = mainContainerResources.MemoryRequest
		} else {
			// 验证资源设置的一致性
			validateResourceConsistency(deploymentMap[key], mainContainerResources)
		}

		// 计算使用量（排除 istio-proxy）
		cpuUsage := parseResourceValueFloat(mainContainerResources.CPUUsageAvg)
		memoryUsage := parseResourceValueFloat(mainContainerResources.MemoryUsageAvg)

		deploymentMap[key].AverageCPUUsage += cpuUsage
		deploymentMap[key].AverageMemoryUsage += memoryUsage

		cpuUsageMax := parseResourceValueFloat(mainContainerResources.CPUUsageMax)
		memoryUsageMax := parseResourceValueFloat(mainContainerResources.MemoryUsageMax)

		deploymentMap[key].MaxCPUUsage = math.Max(deploymentMap[key].MaxCPUUsage, cpuUsageMax)
		deploymentMap[key].MaxMemoryUsage = math.Max(deploymentMap[key].MaxMemoryUsage, memoryUsageMax)
	}

	var recommendations []DeploymentResourceRecommendation
	for _, rec := range deploymentMap {
		if rec.Replicas > 0 {
			rec.AverageCPUUsage /= float64(rec.Replicas)
			rec.AverageMemoryUsage /= float64(rec.Replicas)
		}
		updateDeploymentRecommendation(rec)
		recommendations = append(recommendations, *rec)
	}

	return recommendations
}

func getMainContainerResources(pod PodInfo) ContainerResources {
	// 如果 pod 只有一个容器，直接返回 pod 的资源设置
	if len(pod.ContainerStatuses) <= 1 {
		return ContainerResources{
			CPULimit:       parseResourceValueFloat(pod.CPULimit),
			MemoryLimit:    parseResourceValueFloat(pod.MemoryLimit),
			CPURequest:     parseResourceValueFloat(pod.CPURequest),
			MemoryRequest:  parseResourceValueFloat(pod.MemoryRequest),
			CPUUsageAvg:    pod.CPUUsageAvg,
			MemoryUsageAvg: pod.MemoryUsageAvg,
			CPUUsageMax:    pod.CPUUsageMax,
			MemoryUsageMax: pod.MemoryUsageMax,
		}
	}

	// 如果有多个容器，找到主容器（非 istio-proxy）
	for _, container := range pod.ContainerStatuses {
		if container.Name != "istio-proxy" {
			return ContainerResources{
				CPULimit:       parseResourceValueFloat(container.CPULimit),
				MemoryLimit:    parseResourceValueFloat(container.MemoryLimit),
				CPURequest:     parseResourceValueFloat(container.CPURequest),
				MemoryRequest:  parseResourceValueFloat(container.MemoryRequest),
				CPUUsageAvg:    container.CPUUsageAvg,
				MemoryUsageAvg: container.MemoryUsageAvg,
				CPUUsageMax:    container.CPUUsageMax,
				MemoryUsageMax: container.MemoryUsageMax,
			}
		}
	}

	// 如果没有找到主容器，返回 pod 级别的资源设置
	return ContainerResources{
		CPULimit:       parseResourceValueFloat(pod.CPULimit),
		MemoryLimit:    parseResourceValueFloat(pod.MemoryLimit),
		CPURequest:     parseResourceValueFloat(pod.CPURequest),
		MemoryRequest:  parseResourceValueFloat(pod.MemoryRequest),
		CPUUsageAvg:    pod.CPUUsageAvg,
		MemoryUsageAvg: pod.MemoryUsageAvg,
		CPUUsageMax:    pod.CPUUsageMax,
		MemoryUsageMax: pod.MemoryUsageMax,
	}
}

func validateResourceConsistency(rec *DeploymentResourceRecommendation, resources ContainerResources) {
	if !isCloseEnough(rec.CPULimit, resources.CPULimit) ||
		!isCloseEnough(rec.MemoryLimit, resources.MemoryLimit) ||
		!isCloseEnough(rec.CPURequest, resources.CPURequest) ||
		!isCloseEnough(rec.MemoryRequest, resources.MemoryRequest) {
	}
}

func updateDeploymentRecommendation(rec *DeploymentResourceRecommendation) {
	if rec.AverageCPUUsage <= 0 && rec.AverageMemoryUsage <= 0 {
		rec.CPURequestAdjustment = "无法提供建议：缺少有效的 CPU 使用数据"
		rec.CPULimitAdjustment = "无法提供建议：缺少有效的 CPU 使用数据"
		rec.MemoryRequestAdjustment = "无法提供建议：缺少有效的内存使用数据"
		rec.MemoryLimitAdjustment = "无法提供建议：缺少有效的内存使用数据"
		return
	}

	// CPU 建议
	if rec.AverageCPUUsage > 0 {
		cpuRequestAdjustment := math.Max(rec.AverageCPUUsage*0.3, 0.01)
		cpuLimitAdjustment := math.Max(rec.MaxCPUUsage*1.5, 0.1)

		if isCloseEnough(cpuRequestAdjustment, rec.CPURequest) {
			rec.CPURequestAdjustment = "当前 CPU 请求设置合理，无需调整"
		} else {
			rec.CPURequestAdjustment = fmt.Sprintf("建议将 CPU 请求调整为 %s", formatCPUValue(cpuRequestAdjustment))
		}

		if isCloseEnough(cpuLimitAdjustment, rec.CPULimit) {
			rec.CPULimitAdjustment = "当前 CPU 限制设置合理，无需调整"
		} else {
			rec.CPULimitAdjustment = fmt.Sprintf("建议将 CPU 限制调整为 %s", formatCPUValue(cpuLimitAdjustment))
		}
	} else {
		rec.CPURequestAdjustment = "无法提供建议：缺少有效的 CPU 使用数据"
		rec.CPULimitAdjustment = "无法提供建议：缺少有效的 CPU 使用数据"
	}

	// 内存建议
	if rec.AverageMemoryUsage > 0 {
		memoryRequestAdjustment := math.Max(rec.AverageMemoryUsage*0.3, 32)
		memoryLimitAdjustment := math.Max(rec.MaxMemoryUsage*1.2, 64) // 使用最大值的1.2倍作为限制

		if isCloseEnough(memoryRequestAdjustment, rec.MemoryRequest) {
			rec.MemoryRequestAdjustment = "当前内存请求设置合理，无需调整"
		} else {
			rec.MemoryRequestAdjustment = fmt.Sprintf("建议将内存请求调整为 %s", formatMemoryValue(memoryRequestAdjustment))
		}

		if isCloseEnough(memoryLimitAdjustment, rec.MemoryLimit) {
			rec.MemoryLimitAdjustment = "当前内存限制设置合理，无需调整"
		} else {
			rec.MemoryLimitAdjustment = fmt.Sprintf("建议将内存限制调整为 %s", formatMemoryValue(memoryLimitAdjustment))
		}
	} else {
		rec.MemoryRequestAdjustment = "无法提供建议：缺少有效的内存使用数据"
		rec.MemoryLimitAdjustment = "无法提供建议：缺少有效的内存使用数据"
	}
}

func isCloseEnough(a, b float64) bool {
	if a == 0 || b == 0 {
		return a == b
	}
	return math.Abs(a-b) <= 0.1*math.Max(a, b)
}

// 添加 ResourceStats 结构体定义
type ResourceStats struct {
	Max float64
	Min float64
	Avg float64
}

func formatCPUValue(value float64) string {
	if value < 1 {
		return fmt.Sprintf("%dm", int(math.Ceil(value*1000)))
	}
	return fmt.Sprintf("%.2f", value)
}

func formatMemoryValue(value float64) string {
	if value >= 1024 {
		return fmt.Sprintf("%.2fGi", value/1024)
	}
	return fmt.Sprintf("%dMi", int(math.Ceil(value)))
}

func parseResourceValue(value string) (float64, bool) {
	if value == "" || value == "0" || value == "0.00 cores" || value == "0.00 MiB" {
		return 0, false
	}
	// 移除单位并解析为浮点数
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, "cores")
	value = strings.TrimSuffix(value, "MiB")
	value = strings.TrimSpace(value)
	parsedValue, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, false
	}
	return parsedValue, true
}

func parseResourceValueFloat(value string) float64 {
	parsedValue, _ := parseResourceValue(value)
	return parsedValue
}

func DebugSinglePodUsage(w http.ResponseWriter, r *http.Request, promClient v1.API, clientset *kubernetes.Clientset) {
	// 从查询参数中获取 pod 信息
	clusterName := r.URL.Query().Get("cluster")
	namespace := r.URL.Query().Get("namespace")
	podName := r.URL.Query().Get("pod")

	if clusterName == "" || namespace == "" || podName == "" {
		http.Error(w, "Missing required parameters", http.StatusBadRequest)
		return
	}

	// 获取 Pod 对象
	pod, err := clientset.CoreV1().Pods(namespace).Get(r.Context(), podName, metav1.GetOptions{})
	if err != nil {
		http.Error(w, fmt.Sprintf("Error getting pod: %v", err), http.StatusInternalServerError)
		return
	}

	endTime := time.Now()
	startTime := getAdjustedStartTime(pod.CreationTimestamp.Time, endTime)

	// 执行 CPU 查询
	cpuMaxResult, _, err := promClient.QueryRange(r.Context(), buildCPUQuery([]string{podName}, "max"), v1.Range{
		Start: startTime,
		End:   endTime,
		Step:  time.Minute * 5,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Error querying max CPU usage: %v", err), http.StatusInternalServerError)
		return
	}

	cpuMinResult, _, err := promClient.QueryRange(r.Context(), buildCPUQuery([]string{podName}, "min"), v1.Range{
		Start: startTime,
		End:   endTime,
		Step:  time.Minute * 5,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Error querying min CPU usage: %v", err), http.StatusInternalServerError)
		return
	}

	cpuAvgResult, _, err := promClient.QueryRange(r.Context(), buildCPUQuery([]string{podName}, "avg"), v1.Range{
		Start: startTime,
		End:   endTime,
		Step:  time.Minute * 5,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Error querying avg CPU usage: %v", err), http.StatusInternalServerError)
		return
	}

	// 执行内存查询
	memMaxResult, _, err := promClient.QueryRange(r.Context(), buildMemoryQuery([]string{podName}, "max"), v1.Range{
		Start: startTime,
		End:   endTime,
		Step:  time.Minute * 5,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Error querying max memory usage: %v", err), http.StatusInternalServerError)
		return
	}

	memMinResult, _, err := promClient.QueryRange(r.Context(), buildMemoryQuery([]string{podName}, "min"), v1.Range{
		Start: startTime,
		End:   endTime,
		Step:  time.Minute * 5,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Error querying min memory usage: %v", err), http.StatusInternalServerError)
		return
	}

	memAvgResult, _, err := promClient.QueryRange(r.Context(), buildMemoryQuery([]string{podName}, "avg"), v1.Range{
		Start: startTime,
		End:   endTime,
		Step:  time.Minute * 5,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("Error querying avg memory usage: %v", err), http.StatusInternalServerError)
		return
	}

	// 处理结果
	cpuUsageMap := processPromQLResults(cpuMaxResult, cpuMinResult, cpuAvgResult)
	memoryUsageMap := processPromQLResults(memMaxResult, memMinResult, memAvgResult)

	// 转换为 PodInfo
	podInfo := convertSinglePodToPodInfo(pod, clientset, cpuUsageMap, memoryUsageMap)

	// 构建响应
	response := map[string]interface{}{
		"pod_info": podInfo,
	}

	// 返回 JSON 响应
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func processPromQLResults(maxResult, minResult, avgResult model.Value) map[string]map[string]UsageStats {
	usageMap := make(map[string]map[string]UsageStats)

	processResult := func(result model.Value, statType string) {
		if matrix, ok := result.(model.Matrix); ok {
			for _, sampleStream := range matrix {
				pod := string(sampleStream.Metric["pod"])
				container := string(sampleStream.Metric["container"])

				if _, exists := usageMap[pod]; !exists {
					usageMap[pod] = make(map[string]UsageStats)
				}

				stats := usageMap[pod][container]
				for _, sample := range sampleStream.Values {
					value := float64(sample.Value)
					switch statType {
					case "max":
						if value > stats.Max {
							stats.Max = value
						}
					case "min":
						if stats.Min == 0 || value < stats.Min {
							stats.Min = value
						}
					case "avg":
						stats.Avg += value
					}
				}
				if statType == "avg" {
					stats.Avg /= float64(len(sampleStream.Values))
				}
				usageMap[pod][container] = stats
			}
		}
	}

	processResult(maxResult, "max")
	processResult(minResult, "min")
	processResult(avgResult, "avg")

	return usageMap
}

func getAdjustedStartTime(podStartTime, endTime time.Time) time.Time {
	podAge := endTime.Sub(podStartTime)
	if podAge < time.Hour {
		return podStartTime
	}
	return endTime.Add(-time.Hour)
}

func CleanupNonExistentPods(clusterName string, clientset *kubernetes.Clientset) error {
	// 获取当前集群中所有活跃的 Pod
	pods, err := clientset.CoreV1().Pods("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	activePods := make(map[string]bool)
	for _, pod := range pods.Items {
		key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		activePods[key] = true
	}

	// 获取最新的时间戳
	latestTimestamp, err := db.GetLatestPodInfoTimestamp(clusterName)
	if err != nil {
		return fmt.Errorf("failed to get latest pod info timestamp: %w", err)
	}

	// 从数据库中获取最新时间戳的所有 Pod 记录
	allPodInfos, err := db.GetPodInfoForTimestamp(clusterName, latestTimestamp)
	if err != nil {
		return fmt.Errorf("failed to get pod info from database: %w", err)
	}

	// 找出需要删除的 Pod
	var podsToDelete []string
	for _, podInfo := range allPodInfos {
		key := fmt.Sprintf("%s/%s", podInfo.Namespace, podInfo.Name)
		if !activePods[key] {
			podsToDelete = append(podsToDelete, key)
		}
	}

	if len(podsToDelete) > 0 {
		err = db.DeleteLatestPodInfoBatch(clusterName, podsToDelete, latestTimestamp)
		if err != nil {
			return fmt.Errorf("failed to delete non-existent pods: %w", err)
		}
	}

	return nil
}
