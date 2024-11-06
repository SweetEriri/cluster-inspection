package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"gopkg.in/yaml.v2"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/client-go/util/retry"
)

// Kubernetes related functions and variables
var (
	kubeClients map[string]*kubernetes.Clientset
	kubeConfigs map[string]*rest.Config
	clientMutex sync.RWMutex
)

func init() {
	kubeClients = make(map[string]*kubernetes.Clientset)
	kubeConfigs = make(map[string]*rest.Config)
}

func GetKubernetesClient(clusterName string, config *Config) (*kubernetes.Clientset, *rest.Config, error) {
	clientMutex.RLock()
	client, clientExists := kubeClients[clusterName]
	kubeConfig, configExists := kubeConfigs[clusterName]
	clientMutex.RUnlock()

	if clientExists && configExists {
		return client, kubeConfig, nil
	}

	clientMutex.Lock()
	defer clientMutex.Unlock()

	// Double-check after acquiring the lock
	if client, clientExists = kubeClients[clusterName]; clientExists {
		kubeConfig = kubeConfigs[clusterName]
		return client, kubeConfig, nil
	}

	// Find the cluster configuration
	var clusterConfig *ClusterConfig
	for _, cluster := range config.Clusters {
		if cluster.ClusterName == clusterName {
			clusterConfig = &cluster
			break
		}
	}
	if clusterConfig == nil {
		return nil, nil, fmt.Errorf("cluster %s not found in configuration", clusterName)
	}

	// 创建 Kubernetes 客户端
	kubeConfig, err := clientcmd.BuildConfigFromFlags("", clusterConfig.Kubeconfig)
	if err != nil {
		return nil, nil, fmt.Errorf("error building kubeconfig: %v", err)
	}

	// 添加限流配置
	kubeConfig.QPS = 50    // 每秒查询数
	kubeConfig.Burst = 100 // 突发查询数

	// 使用自定义的 RateLimiter
	kubeConfig.RateLimiter = &LoggingRateLimiter{
		RateLimiter: flowcontrol.NewTokenBucketRateLimiter(kubeConfig.QPS, kubeConfig.Burst),
	}

	client, err = kubernetes.NewForConfig(kubeConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating Kubernetes client: %v", err)
	}

	// 存储客户端和配置
	kubeClients[clusterName] = client
	kubeConfigs[clusterName] = kubeConfig

	return client, kubeConfig, nil
}

// Custom RateLimiter
type LoggingRateLimiter struct {
	flowcontrol.RateLimiter
}

func (l *LoggingRateLimiter) Wait(ctx context.Context) error {
	start := time.Now()
	err := l.RateLimiter.Wait(ctx)
	duration := time.Since(start)

	if err != nil {
		log.Printf("Rate limited: %v, waited for %v", err, duration)
	}
	return err
}

// Prometheus related functions
func CreatePromClient(prometheusURL, token string) (v1.API, error) {
	client, err := api.NewClient(api.Config{
		Address: prometheusURL,
		RoundTripper: &tokenRoundTripper{
			token: token,
			rt:    http.DefaultTransport,
		},
	})
	if err != nil {
		return nil, err
	}

	return v1.NewAPI(client), nil
}

type tokenRoundTripper struct {
	token string
	rt    http.RoundTripper
}

func (t *tokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	return t.rt.RoundTrip(req)
}

func QueryPrometheus(query string, promClient v1.API) (interface{}, error) {
	result, warnings, err := promClient.Query(context.TODO(), query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("error querying Prometheus: %v", err)
	}
	if len(warnings) > 0 {
		log.Printf("Warnings: %v", warnings)
	}

	return result, nil
}

// Resource formatting functions
func FormatCPU(q resource.Quantity) string {
	return fmt.Sprintf("%.2f", float64(q.MilliValue())/1000)
}

func FormatMemory(q resource.Quantity) string {
	return fmt.Sprintf("%.2f MiB", float64(q.ScaledValue(resource.Mega)))
}

func CalculatePercentage(used, total resource.Quantity) string {
	usedVal := used.AsApproximateFloat64()
	totalVal := total.AsApproximateFloat64()

	if totalVal == 0 {
		return "N/A"
	}

	percentage := (usedVal / totalVal) * 100
	return fmt.Sprintf("%.2f%%", percentage)
}

func FormatCPUUsage(cpuUsage float64) string {
	return fmt.Sprintf("%.2f cores", cpuUsage)
}

func FormatMemoryUsage(memoryUsage float64) string {
	return fmt.Sprintf("%.2f MiB", memoryUsage)
}

func CalculatePrometheusPercentage(used, total float64) float64 {
	if total == 0 {
		return 0
	}
	percentage := (used / total) * 100
	return percentage
}

func FormatBytes(bytes float64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%.2f B", bytes)
	}
	div, exp := unit, 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB", "EB"}
	return fmt.Sprintf("%.2f %s", bytes/float64(div), units[exp])
}

// DingTalk related structs and functions
type DingTalkMessage struct {
	MsgType string           `json:"msgtype"`
	Text    DingTalkTextData `json:"text"`
}

type DingTalkTextData struct {
	Content string `json:"content"`
}

func SendDingTalkMessage(webhookURL string, message string) error {
	msgData := DingTalkMessage{
		MsgType: "text",
		Text: DingTalkTextData{
			Content: message,
		},
	}

	msgBytes, err := json.Marshal(msgData)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %v", err)
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(msgBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received non-200 response: %s", resp.Status)
	}

	return nil
}

// Utility functions
func GetCurrentTimestamp() string {
	return time.Now().Format("20060102_150405")
}

func SanitizeFloat(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return "0"
	}
	return fmt.Sprintf("%.2f", value)
}

func RetryKubernetesOperation(operation func() error) error {
	return retry.OnError(retry.DefaultRetry, func(err error) bool {
		return true
	}, operation)
}

func SanitizeFloatValue(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return value
}

type ClusterConfig struct {
	Name            string `yaml:"name"`
	Kubeconfig      string `yaml:"kubeconfig"`
	Prometheus      string `yaml:"prometheus"`
	PrometheusToken string `yaml:"prometheus_token"`
	ClusterName     string `yaml:"ClusterName"`
	CustomID        string // 备用
}

type Config struct {
	Clusters          []ClusterConfig `yaml:"clusters"`
	ClusterIdentifier string          `yaml:"ClusterIdentifier"`
	Database          DBConfig        `yaml:"database"`
}

type DBConfig struct {
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Host     string `yaml:"host"`
	Port     string `yaml:"port"`
	DBName   string `yaml:"dbname"`
}

func LoadConfig(configPath string) (*Config, error) {
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	var config Config
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

func MapToString(m map[string]string) string {
	pairs := []string{}
	for k, v := range m {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(pairs, ",")
}

func StructToString(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

const TimeLayout = "2006-01-02T15:04:05"

func ParseLocalTime(timeStr string) (time.Time, error) {
	return time.ParseInLocation(TimeLayout, timeStr, time.Local)
}

func StringToMap(s string) map[string]string {
	if s == "" {
		return nil
	}

	var result map[string]string
	err := json.Unmarshal([]byte(s), &result)
	if err != nil {
		log.Printf("Error unmarshalling string to map: %v", err)
		return nil
	}

	return result
}

func ParseCPUValue(cpuStr string) (float64, error) {
	cpuQuantity, err := resource.ParseQuantity(cpuStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse CPU value: %w", err)
	}
	return float64(cpuQuantity.MilliValue()) / 1000, nil
}

func ParseMemoryValue(memStr string) (float64, error) {
	memQuantity, err := resource.ParseQuantity(memStr)
	if err != nil {
		return 0, fmt.Errorf("failed to parse memory value: %w", err)
	}
	return float64(memQuantity.Value()), nil
}

func Average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func Max(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	max := values[0]
	for _, v := range values[1:] {
		if v > max {
			max = v
		}
	}
	return max
}
