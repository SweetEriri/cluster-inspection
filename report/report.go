package report

import (
	"cluster-inspection/db"
	"cluster-inspection/handlers"
	"cluster-inspection/utils"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

type ReportHandler struct {
	config           *utils.Config
	promClients      map[string]v1.API
	clusterNameIndex map[string]int
}

type ClusterFullReport struct {
	Nodes     []handlers.NodeInfo  `json:"nodes"`
	Events    []handlers.EventInfo `json:"events"`
	Pods      []handlers.PodInfo   `json:"pods"`
	TotalPods int                  `json:"totalPods"`
}

func NewReportHandler(config *utils.Config) (*ReportHandler, error) {
	promClients := make(map[string]v1.API)
	clusterNameIndex := make(map[string]int)
	for i, cluster := range config.Clusters {
		promClient, err := utils.CreatePromClient(cluster.Prometheus, cluster.PrometheusToken)
		if err != nil {
			return nil, fmt.Errorf("failed to create Prometheus client for cluster %s: %w", cluster.ClusterName, err)
		}
		promClients[cluster.ClusterName] = promClient
		clusterNameIndex[cluster.ClusterName] = i
	}
	return &ReportHandler{
		config:           config,
		promClients:      promClients,
		clusterNameIndex: clusterNameIndex,
	}, nil
}

func (h *ReportHandler) GetNodeReport(w http.ResponseWriter, r *http.Request) {
	log.Println("Starting GetNodeReport")
	clusterName := h.getClusterNameFromRequest(r)

	if clusterName == "" {
		h.getAllClustersNodeReport(w)
		return
	}

	if !h.clusterExists(clusterName) {
		h.handleError(w, fmt.Errorf("cluster %s not found", clusterName), http.StatusNotFound)
		return
	}

	nodes, err := handlers.GetNodeResourceInfo(clusterName, h.promClients[clusterName], h.config)
	if err != nil {
		h.handleError(w, err, http.StatusInternalServerError)
		return
	}

	log.Printf("Retrieved nodes from cluster: %s", clusterName)
	h.sendJSONResponse(w, map[string][]handlers.NodeInfo{clusterName: nodes})
}

func (h *ReportHandler) GetEventReport(w http.ResponseWriter, r *http.Request) {
	log.Println("Starting GetEventReport")
	clusterName := h.getClusterNameFromRequest(r)

	if clusterName == "" {
		h.getAllClustersEventReport(w)
		return
	}

	if !h.clusterExists(clusterName) {
		h.handleError(w, fmt.Errorf("cluster %s not found", clusterName), http.StatusNotFound)
		return
	}

	events, err := handlers.GetEventInfo(clusterName, h.config)
	if err != nil {
		h.handleError(w, err, http.StatusInternalServerError)
		return
	}

	log.Printf("Retrieved events from cluster: %s", clusterName)
	h.sendJSONResponse(w, map[string][]handlers.EventInfo{clusterName: events})
}

func (h *ReportHandler) GetPodReport(w http.ResponseWriter, r *http.Request) {
	log.Println("Starting GetPodReport")
	clusterName := h.getClusterNameFromRequest(r)
	page, pageSize := getPageParams(r)

	if clusterName == "" {
		h.getAllClustersPodReport(w, page, pageSize)
		return
	}

	if !h.clusterExists(clusterName) {
		h.handleError(w, fmt.Errorf("cluster %s not found", clusterName), http.StatusNotFound)
		return
	}

	var pods []handlers.PodInfo
	var totalPods int
	var err error

	if pageSize == -1 {
		// 获取所有 pod
		pods, totalPods, err = handlers.GetPodResourceInfo(clusterName, h.promClients[clusterName], h.config, 1, 0)
	} else {
		pods, totalPods, err = handlers.GetPodResourceInfo(clusterName, h.promClients[clusterName], h.config, page, pageSize)
	}

	if err != nil {
		h.handleError(w, err, http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"pods":      pods,
		"totalPods": totalPods,
	}

	log.Printf("Retrieved %d pods from cluster: %s (Total: %d)", len(pods), clusterName, totalPods)
	h.sendJSONResponse(w, response)
}

func (h *ReportHandler) GetFullReport(w http.ResponseWriter, r *http.Request) {
	log.Println("Starting GetFullReport")
	clusterName := h.getClusterNameFromRequest(r)

	if clusterName == "" {
		h.getAllClustersFullReport(w)
		return
	}

	if !h.clusterExists(clusterName) {
		h.handleError(w, fmt.Errorf("cluster %s not found", clusterName), http.StatusNotFound)
		return
	}

	fullReport, err := h.getClusterFullReport(clusterName)
	if err != nil {
		h.handleError(w, err, http.StatusInternalServerError)
		return
	}

	log.Printf("Retrieved full report from cluster: %s", clusterName)
	h.sendJSONResponse(w, fullReport)
}

func (h *ReportHandler) getClusterNameFromRequest(r *http.Request) string {
	clusterName := r.URL.Query().Get("cluster")
	if clusterName != "" {
		return clusterName
	}

	parts := strings.Split(r.URL.Path, "/")
	if len(parts) >= 5 {
		return parts[4]
	}

	return ""
}

func (h *ReportHandler) getAllClustersNodeReport(w http.ResponseWriter) {
	log.Println("Starting GetNodeReport for all clusters")
	allNodes := make(map[string][]handlers.NodeInfo)
	var wg sync.WaitGroup
	var mu sync.Mutex
	errChan := make(chan error, len(h.config.Clusters))

	for _, cluster := range h.config.Clusters {
		wg.Add(1)
		go func(cluster utils.ClusterConfig) {
			defer wg.Done()
			nodes, err := handlers.GetNodeResourceInfo(cluster.ClusterName, h.promClients[cluster.ClusterName], h.config)
			if err != nil {
				errChan <- err
				return
			}
			mu.Lock()
			allNodes[cluster.ClusterName] = nodes
			mu.Unlock()
		}(cluster)
	}

	wg.Wait()
	close(errChan)

	if err := <-errChan; err != nil {
		h.handleError(w, err, http.StatusInternalServerError)
		return
	}

	log.Printf("Retrieved nodes from %d clusters", len(allNodes))
	h.sendJSONResponse(w, allNodes)
}

func (h *ReportHandler) getAllClustersEventReport(w http.ResponseWriter) {
	log.Println("Starting GetEventReport for all clusters")
	allEvents := make(map[string][]handlers.EventInfo)
	var wg sync.WaitGroup
	var mu sync.Mutex
	errChan := make(chan error, len(h.config.Clusters))

	for _, cluster := range h.config.Clusters {
		wg.Add(1)
		go func(cluster utils.ClusterConfig) {
			defer wg.Done()
			events, err := handlers.GetEventInfo(cluster.ClusterName, h.config)
			if err != nil {
				errChan <- err
				return
			}
			mu.Lock()
			allEvents[cluster.ClusterName] = events
			mu.Unlock()
		}(cluster)
	}

	wg.Wait()
	close(errChan)

	if err := <-errChan; err != nil {
		h.handleError(w, err, http.StatusInternalServerError)
		return
	}

	log.Printf("Retrieved events from %d clusters", len(allEvents))
	h.sendJSONResponse(w, allEvents)
}

func (h *ReportHandler) getAllClustersPodReport(w http.ResponseWriter, page, pageSize int) {
	log.Println("Starting GetPodReport for all clusters")
	allPods := make(map[string]map[string]interface{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	errChan := make(chan error, len(h.config.Clusters))

	for _, cluster := range h.config.Clusters {
		wg.Add(1)
		go func(cluster utils.ClusterConfig) {
			defer wg.Done()
			var pods []handlers.PodInfo
			var totalPods int
			var err error

			if pageSize == -1 {
				pods, totalPods, err = handlers.GetPodResourceInfo(cluster.ClusterName, h.promClients[cluster.ClusterName], h.config, 1, 0)
			} else {
				pods, totalPods, err = handlers.GetPodResourceInfo(cluster.ClusterName, h.promClients[cluster.ClusterName], h.config, page, pageSize)
			}

			if err != nil {
				errChan <- err
				return
			}

			mu.Lock()
			allPods[cluster.ClusterName] = map[string]interface{}{
				"pods":      pods,
				"totalPods": totalPods,
			}
			mu.Unlock()
		}(cluster)
	}

	wg.Wait()
	close(errChan)

	if err := <-errChan; err != nil {
		h.handleError(w, err, http.StatusInternalServerError)
		return
	}

	log.Printf("Retrieved pods from %d clusters", len(allPods))
	h.sendJSONResponse(w, allPods)
}

func (h *ReportHandler) getAllClustersFullReport(w http.ResponseWriter) {
	log.Println("Starting GetFullReport for all clusters")
	fullReport := make(map[string]ClusterFullReport)
	var wg sync.WaitGroup
	var mu sync.Mutex
	errChan := make(chan error, len(h.config.Clusters))

	for _, cluster := range h.config.Clusters {
		wg.Add(1)
		go func(cluster utils.ClusterConfig) {
			defer wg.Done()
			clusterReport, err := h.getClusterFullReport(cluster.ClusterName)
			if err != nil {
				errChan <- err
				return
			}
			mu.Lock()
			fullReport[cluster.ClusterName] = clusterReport
			mu.Unlock()
		}(cluster)
	}

	wg.Wait()
	close(errChan)

	if err := <-errChan; err != nil {
		h.handleError(w, err, http.StatusInternalServerError)
		return
	}

	log.Printf("Retrieved full report from %d clusters", len(fullReport))
	h.sendJSONResponse(w, fullReport)
}

func (h *ReportHandler) getClusterFullReport(clusterName string) (ClusterFullReport, error) {
	nodes, err := handlers.GetNodeResourceInfo(clusterName, h.promClients[clusterName], h.config)
	if err != nil {
		return ClusterFullReport{}, fmt.Errorf("error in GetNodeResourceInfo: %w", err)
	}

	events, err := handlers.GetEventInfo(clusterName, h.config)
	if err != nil {
		return ClusterFullReport{}, fmt.Errorf("error in GetEventInfo: %w", err)
	}

	pods, totalPods, err := handlers.GetPodResourceInfo(clusterName, h.promClients[clusterName], h.config, 1, 1000000)
	if err != nil {
		return ClusterFullReport{}, fmt.Errorf("error in GetPodResourceInfo: %w", err)
	}

	return ClusterFullReport{
		Nodes:     nodes,
		Events:    events,
		Pods:      pods,
		TotalPods: totalPods,
	}, nil
}

func (h *ReportHandler) sendJSONResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *ReportHandler) handleError(w http.ResponseWriter, err error, statusCode int) {
	log.Printf("Error: %v", err)
	http.Error(w, err.Error(), statusCode)
}

func (h *ReportHandler) clusterExists(clusterName string) bool {
	_, exists := h.clusterNameIndex[clusterName]
	return exists
}

func getPageParams(r *http.Request) (int, int) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))

	if page < 1 {
		page = 1
	}

	// 如果 pageSize 为 0，我们返回 -1 表示需要获取所有 pod
	if pageSize == 0 {
		return page, -1
	} else if pageSize < 1 {
		pageSize = 10 // 默认页面大小
	}

	return page, pageSize
}

func (h *ReportHandler) GetAvailableClusters(w http.ResponseWriter, r *http.Request) {
	log.Println("Starting GetAvailableClusters")

	clusters := make([]string, 0, len(h.config.Clusters))
	for _, cluster := range h.config.Clusters {
		clusters = append(clusters, cluster.ClusterName)
	}

	log.Printf("Retrieved %d available clusters", len(clusters))
	h.sendJSONResponse(w, map[string]interface{}{
		"clusters": clusters,
	})
}

func (h *ReportHandler) GetAllReports(w http.ResponseWriter, r *http.Request) {
	log.Printf("GetAllReports called with URL: %s", r.URL.String())

	query, err := utils.ParseTimeRangeQuery(r)
	if err != nil {
		http.Error(w, "Invalid query parameters", http.StatusBadRequest)
		return
	}

	reportType := r.URL.Query().Get("type")
	if reportType == "" {
		http.Error(w, "Report type is required", http.StatusBadRequest)
		return
	}

	clusterName := r.URL.Query().Get("cluster")

	var result interface{}

	switch reportType {
	case "node":
		result, err = db.GetNodeInfo(clusterName, 0, 0, query.StartTime, query.EndTime)
	case "pod":
		result, err = db.GetPodInfo(clusterName, "", 0, 0, query.StartTime, query.EndTime)
	case "event":
		result, err = db.GetEventInfo(clusterName, 0, 0, query.StartTime, query.EndTime)
	default:
		http.Error(w, "Invalid report type", http.StatusBadRequest)
		return
	}

	if err != nil {
		log.Printf("Error querying database: %v", err)
		http.Error(w, fmt.Sprintf("Failed to get %s report: %v", reportType, err), http.StatusInternalServerError)
		return
	}

	// 检查结果是否为空
	if reflect.ValueOf(result).Len() == 0 {
		log.Printf("No data found for %s report", reportType)
		http.Error(w, "No data found", http.StatusNotFound)
		return
	}

	log.Printf("Query returned %d records for %s report", reflect.ValueOf(result).Len(), reportType)

	h.sendJSONResponse(w, result)
	log.Printf("Successfully returned %s report", reportType)
}

// 在 ReportHandler 结构体的其他方法之后添加这个新方法

func (h *ReportHandler) DebugSinglePodUsage(w http.ResponseWriter, r *http.Request) {
	log.Println("Starting DebugSinglePodUsage")

	// 从查询参数中获取 pod 信息
	clusterName := r.URL.Query().Get("cluster")
	namespace := r.URL.Query().Get("namespace")
	podName := r.URL.Query().Get("pod")

	if clusterName == "" || namespace == "" || podName == "" {
		h.handleError(w, fmt.Errorf("missing required parameters: cluster, namespace, or pod"), http.StatusBadRequest)
		return
	}

	if !h.clusterExists(clusterName) {
		h.handleError(w, fmt.Errorf("cluster %s not found", clusterName), http.StatusNotFound)
		return
	}

	// 获取对应集群的 Prometheus 客户端
	promClient := h.promClients[clusterName]

	// 设置时间范围（过去24小时）
	endTime := time.Now()
	startTime := endTime.Add(-24 * time.Hour)

	// 构建查询
	cpuQuery := fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{namespace="%s", pod="%s"}[5m])) by (namespace, pod)`, namespace, podName)
	memoryQuery := fmt.Sprintf(`sum(container_memory_working_set_bytes{namespace="%s", pod="%s"}) by (namespace, pod)`, namespace, podName)

	// 执行查询
	cpuResult, _, err := promClient.QueryRange(r.Context(), cpuQuery, v1.Range{
		Start: startTime,
		End:   endTime,
		Step:  time.Minute * 5,
	})
	if err != nil {
		h.handleError(w, fmt.Errorf("error querying CPU usage: %w", err), http.StatusInternalServerError)
		return
	}

	memoryResult, _, err := promClient.QueryRange(r.Context(), memoryQuery, v1.Range{
		Start: startTime,
		End:   endTime,
		Step:  time.Minute * 5,
	})
	if err != nil {
		h.handleError(w, fmt.Errorf("error querying memory usage: %w", err), http.StatusInternalServerError)
		return
	}

	// 处理结果
	cpuUsage := processQueryResult(cpuResult)
	memoryUsage := processQueryResult(memoryResult)

	// 构建响应
	response := map[string]interface{}{
		"pod":          podName,
		"namespace":    namespace,
		"cluster":      clusterName,
		"cpu_usage":    cpuUsage,
		"memory_usage": memoryUsage,
	}

	log.Printf("Retrieved debug info for pod %s in namespace %s of cluster %s", podName, namespace, clusterName)
	h.sendJSONResponse(w, response)
}

func processQueryResult(result model.Value) []map[string]interface{} {
	var processed []map[string]interface{}

	if matrix, ok := result.(model.Matrix); ok {
		for _, sampleStream := range matrix {
			for _, sample := range sampleStream.Values {
				processed = append(processed, map[string]interface{}{
					"timestamp": sample.Timestamp.Time().Unix(),
					"value":     sample.Value,
				})
			}
		}
	}

	return processed
}
