package db

import (
	"fmt"
	"log"
	"strings"
	"time"

	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

var DB *gorm.DB

// NodeInfoDB 结构体
type NodeInfoDB struct {
	ID                     uuid.UUID `gorm:"type:char(36);primary_key"`
	Cluster                string    `gorm:"index:idx_node_cluster_name_time"`
	Name                   string    `gorm:"index:idx_node_cluster_name_time"`
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
	Conditions             string `gorm:"type:text"`
	IsHealthy              bool
	IsCPUUsageCritical     bool
	IsMemoryUsageCritical  bool
	IsDiskUsageCritical    bool
	IsNetworkUsageCritical bool
	Timestamp              time.Time `gorm:"index:idx_node_cluster_name_time"`
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

type NullFloat64 struct {
	sql.NullFloat64
}

func (nf NullFloat64) MarshalJSON() ([]byte, error) {
	if !nf.Valid {
		return []byte("null"), nil
	}
	return json.Marshal(nf.Float64)
}

// PodInfoDB 结体
type PodInfoDB struct {
	ID                     uuid.UUID `gorm:"type:char(36);primary_key"`
	Cluster                string    `gorm:"index:idx_pod_cluster_ns_name_time"`
	Name                   string    `gorm:"index:idx_pod_cluster_ns_name_time;index:idx_pod_name"`
	Namespace              string    `gorm:"index:idx_pod_cluster_ns_name_time"`
	PodIP                  string
	Labels                 string `gorm:"type:text"`
	CPUUsageMax            string
	CPUUsageMin            string
	CPUUsageAvg            string
	MemoryUsageMax         string
	MemoryUsageMin         string
	MemoryUsageAvg         string
	CPURequest             string
	MemoryRequest          string
	CPULimit               string
	MemoryLimit            string
	CPUUsagePercent        NullFloat64 `gorm:"type:decimal(10,4)" json:"cpuUsagePercent"`
	MemoryUsagePercent     NullFloat64 `gorm:"type:decimal(10,4)" json:"memoryUsagePercent"`
	CPUUsagePercentMax     NullFloat64 `gorm:"type:decimal(10,4)" json:"cpuUsagePercentMax"`
	CPUUsagePercentMin     NullFloat64 `gorm:"type:decimal(10,4)" json:"cpuUsagePercentMin"`
	CPUUsagePercentAvg     NullFloat64 `gorm:"type:decimal(10,4)" json:"cpuUsagePercentAvg"`
	MemoryUsagePercentMax  NullFloat64 `gorm:"type:decimal(10,4)" json:"memoryUsagePercentMax"`
	MemoryUsagePercentMin  NullFloat64 `gorm:"type:decimal(10,4)" json:"memoryUsagePercentMin"`
	MemoryUsagePercentAvg  NullFloat64 `gorm:"type:decimal(10,4)" json:"memoryUsagePercentAvg"`
	Status                 string
	RestartCount           int
	IsCPUUsageCritical     bool
	IsMemoryUsageCritical  bool
	IsRestartCountCritical bool
	ContainerStatuses      string `gorm:"type:text"`
	Category               string
	OwnerName              string
	Timestamp              time.Time `gorm:"index:idx_pod_cluster_ns_name_time"`
	CreatedAt              time.Time
	UpdatedAt              time.Time
}

// EventInfoDB 结构体
type EventInfoDB struct {
	ID              uuid.UUID `gorm:"type:char(36);primary_key"`
	Cluster         string    `gorm:"index:idx_event_cluster_time"`
	Type            string    `gorm:"index:idx_event_lookup"`
	Message         string
	Reason          string    `gorm:"index:idx_event_lookup"`
	Time            time.Time `gorm:"not null;index:idx_event_lookup"`
	InvolvedObject  string    `gorm:"type:text"`
	RelatedResource string
	Timestamp       time.Time `gorm:"index:idx_event_cluster_time"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func InitDB(dsn string) error {
	var err error
	DB, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return err
	}

	// 自动迁移
	return DB.AutoMigrate(&NodeInfoDB{}, &PodInfoDB{}, &EventInfoDB{})
}

func SaveNodeInfo(nodeInfo NodeInfoDB) error {
	nodeInfo.ID = uuid.New()
	result := DB.Create(&nodeInfo)
	if result.Error != nil {
		log.Printf("Error saving node info to database: %v", result.Error)
		return result.Error
	}
	log.Printf("Successfully saved node info to database: %s", nodeInfo.Name)
	return nil
}

// GetNodeInfo retrieves node info from the database
func GetNodeInfo(clusterName string, limit, offset int, startTime, endTime time.Time) ([]NodeInfoDB, error) {
	var nodeInfos []NodeInfoDB
	query := DB.Order("timestamp desc")

	if clusterName != "" {
		query = query.Where("cluster = ?", clusterName)
	}

	if !startTime.IsZero() {
		query = query.Where("timestamp >= ?", startTime)
	}
	if !endTime.IsZero() {
		query = query.Where("timestamp <= ?", endTime)
	}

	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	result := query.Find(&nodeInfos)
	return nodeInfos, result.Error
}

// SaveNodeInfoBatch 批量保存或更新节点信息
func SaveNodeInfoBatch(nodeInfos []NodeInfoDB) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		for i, nodeInfo := range nodeInfos {
			// 设置小时级别的时间戳
			nodeInfo.Timestamp = time.Date(
				nodeInfo.Timestamp.Year(),
				nodeInfo.Timestamp.Month(),
				nodeInfo.Timestamp.Day(),
				nodeInfo.Timestamp.Hour(),
				0, 0, 0, nodeInfo.Timestamp.Location(),
			)

			// 检查是否已存在相同 cluster、name 和 timestamp 的记录
			var existingRecord NodeInfoDB
			result := tx.Where("cluster = ? AND name = ? AND timestamp = ?", nodeInfo.Cluster, nodeInfo.Name, nodeInfo.Timestamp).First(&existingRecord)

			if result.Error == gorm.ErrRecordNotFound {
				// 如果记录不存在，创建新记录
				nodeInfo.ID = uuid.New()
				if err := tx.Create(&nodeInfo).Error; err != nil {
					log.Printf("Error creating new node info for %s: %v", nodeInfo.Name, err)
					return fmt.Errorf("failed to create node info %d/%d: %w", i+1, len(nodeInfos), err)
				}
			} else if result.Error != nil {
				// 如果发生其他错误
				log.Printf("Error checking existing record for %s: %v", nodeInfo.Name, result.Error)
				return fmt.Errorf("failed to check existing record for node info %d/%d: %w", i+1, len(nodeInfos), result.Error)
			} else {
				// 如果记录存在，更新现有录
				if err := tx.Model(&existingRecord).Updates(map[string]interface{}{
					"cpu_usage":                 nodeInfo.CPUUsage,
					"cpu_usage_percent":         nodeInfo.CPUUsagePercent,
					"memory_usage":              nodeInfo.MemoryUsage,
					"memory_usage_percent":      nodeInfo.MemoryUsagePercent,
					"disk_usage":                nodeInfo.DiskUsage,
					"disk_usage_percent":        nodeInfo.DiskUsagePercent,
					"load1":                     nodeInfo.Load1,
					"load5":                     nodeInfo.Load5,
					"load15":                    nodeInfo.Load15,
					"network_receive":           nodeInfo.NetworkReceive,
					"network_transmit":          nodeInfo.NetworkTransmit,
					"conditions":                nodeInfo.Conditions,
					"is_healthy":                nodeInfo.IsHealthy,
					"is_cpu_usage_critical":     nodeInfo.IsCPUUsageCritical,
					"is_memory_usage_critical":  nodeInfo.IsMemoryUsageCritical,
					"is_disk_usage_critical":    nodeInfo.IsDiskUsageCritical,
					"is_network_usage_critical": nodeInfo.IsNetworkUsageCritical,
					"updated_at":                time.Now(),
				}).Error; err != nil {
					log.Printf("Error updating node info for %s: %v", nodeInfo.Name, err)
					return fmt.Errorf("failed to update node info %d/%d: %w", i+1, len(nodeInfos), err)
				}
			}
		}
		log.Printf("Successfully processed all %d node infos in the transaction", len(nodeInfos))
		return nil
	})
}

// SavePodInfoBatch 批量保存或更新 Pod 信息
func SavePodInfoBatch(podInfos []PodInfoDB) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		for i, podInfo := range podInfos {
			podInfo.Timestamp = time.Date(
				podInfo.Timestamp.Year(),
				podInfo.Timestamp.Month(),
				podInfo.Timestamp.Day(),
				podInfo.Timestamp.Hour(),
				0, 0, 0, podInfo.Timestamp.Location(),
			)

			var existingRecord PodInfoDB
			result := tx.Where("cluster = ? AND namespace = ? AND name = ? AND timestamp = ?", podInfo.Cluster, podInfo.Namespace, podInfo.Name, podInfo.Timestamp).First(&existingRecord)

			if result.Error == gorm.ErrRecordNotFound {
				podInfo.ID = uuid.New()
				if err := tx.Create(&podInfo).Error; err != nil {
					return fmt.Errorf("failed to create pod info %d/%d: %w", i+1, len(podInfos), err)
				}
			} else if result.Error != nil {
				return fmt.Errorf("failed to check existing record for pod info %d/%d: %w", i+1, len(podInfos), result.Error)
			} else {
				if err := tx.Model(&existingRecord).Updates(podInfo).Error; err != nil {
					return fmt.Errorf("failed to update pod info %d/%d: %w", i+1, len(podInfos), err)
				}
			}
		}
		return nil
	})
}

// GetPodInfo retrieves pod info from the database
func GetPodInfo(clusterName, podName string, limit, offset int, startTime, endTime time.Time) ([]PodInfoDB, error) {
	var podInfos []PodInfoDB
	query := DB.Order("timestamp desc")

	if clusterName != "" {
		query = query.Where("cluster = ?", clusterName)
	}

	if podName != "" {
		query = query.Where("name = ?", podName)
	}

	if !startTime.IsZero() {
		query = query.Where("timestamp >= ?", startTime)
	}
	if !endTime.IsZero() {
		query = query.Where("timestamp <= ?", endTime)
	}

	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	result := query.Find(&podInfos)
	return podInfos, result.Error
}

// SaveEventInfoBatch 批量保存或更新事件信息
func SaveEventInfoBatch(eventInfos []EventInfoDB) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		for i, eventInfo := range eventInfos {
			eventInfo.Timestamp = time.Date(
				eventInfo.Timestamp.Year(),
				eventInfo.Timestamp.Month(),
				eventInfo.Timestamp.Day(),
				eventInfo.Timestamp.Hour(),
				0, 0, 0, eventInfo.Timestamp.Location(),
			)

			var existingRecord EventInfoDB
			result := tx.Where("cluster = ? AND type = ? AND message = ? AND reason = ? AND time = ?", eventInfo.Cluster, eventInfo.Type, eventInfo.Message, eventInfo.Reason, eventInfo.Time).First(&existingRecord)

			if result.Error == gorm.ErrRecordNotFound {
				eventInfo.ID = uuid.New()
				if err := tx.Create(&eventInfo).Error; err != nil {
					return fmt.Errorf("failed to create event info %d/%d: %w", i+1, len(eventInfos), err)
				}
			} else if result.Error != nil {
				return fmt.Errorf("failed to check existing record for event info %d/%d: %w", i+1, len(eventInfos), result.Error)
			} else {
				if err := tx.Model(&existingRecord).Updates(eventInfo).Error; err != nil {
					return fmt.Errorf("failed to update event info %d/%d: %w", i+1, len(eventInfos), err)
				}
			}
		}
		return nil
	})
}

// GetEventInfo 从数据库中检索事件信息
func GetEventInfo(clusterName string, limit, offset int, startTime, endTime time.Time) ([]EventInfoDB, error) {
	var eventInfos []EventInfoDB
	query := DB.Order("timestamp desc")

	if clusterName != "" {
		query = query.Where("cluster = ?", clusterName)
	}

	if !startTime.IsZero() {
		query = query.Where("timestamp >= ?", startTime)
	}
	if !endTime.IsZero() {
		query = query.Where("timestamp <= ?", endTime)
	}

	if limit > 0 {
		query = query.Limit(limit)
	}
	if offset > 0 {
		query = query.Offset(offset)
	}

	result := query.Find(&eventInfos)
	return eventInfos, result.Error
}

// GetLatestPodInfoTimestamp 获取最新的 Pod 信息时间戳
func GetLatestPodInfoTimestamp(clusterName string) (time.Time, error) {
	var latestTimestamp time.Time
	err := DB.Model(&PodInfoDB{}).
		Where("cluster = ?", clusterName).
		Order("timestamp DESC").
		Limit(1).
		Pluck("timestamp", &latestTimestamp).Error
	return latestTimestamp, err
}

// GetPodInfoForTimestamp 获取指定时间戳的所有 Pod 信息
func GetPodInfoForTimestamp(clusterName string, timestamp time.Time) ([]PodInfoDB, error) {
	var podInfos []PodInfoDB
	result := DB.Where("cluster = ? AND timestamp = ?", clusterName, timestamp).Find(&podInfos)
	return podInfos, result.Error
}

// DeleteLatestPodInfoBatch 批量删除最新时间戳的 Pod 信息
func DeleteLatestPodInfoBatch(clusterName string, podKeys []string, timestamp time.Time) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		for _, podKey := range podKeys {
			parts := strings.Split(podKey, "/")
			if len(parts) != 2 {
				return fmt.Errorf("invalid pod key: %s", podKey)
			}
			namespace, name := parts[0], parts[1]
			if err := tx.Where("cluster = ? AND namespace = ? AND name = ? AND timestamp = ?",
				clusterName, namespace, name, timestamp).Delete(&PodInfoDB{}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
