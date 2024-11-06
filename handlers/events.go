package handlers

import (
	"cluster-inspection/db"
	"cluster-inspection/utils"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type EventInfo struct {
	Type            string
	Message         string
	Reason          string
	Time            metav1.Time
	InvolvedObject  corev1.ObjectReference
	RelatedResource string
}

func GetEventInfo(clusterName string, config *utils.Config) ([]EventInfo, error) {
	// 使用 clusterName 获取特定集群的 Kubernetes 客户端
	clientset, _, err := utils.GetKubernetesClient(clusterName, config)
	if err != nil {
		return nil, fmt.Errorf("failed to get Kubernetes client for cluster %s: %v", clusterName, err)
	}

	events, err := clientset.CoreV1().Events("").List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list events: %v", err)
	}

	var eventInfos []EventInfo
	for _, event := range events.Items {
		relatedResource := getRelatedResource(event.InvolvedObject, clusterName, config)
		eventInfo := EventInfo{
			Type:            event.Type,
			Message:         event.Message,
			Reason:          event.Reason,
			Time:            event.LastTimestamp,
			InvolvedObject:  event.InvolvedObject,
			RelatedResource: relatedResource,
		}
		eventInfos = append(eventInfos, eventInfo)
	}

	var eventInfoDBs []db.EventInfoDB
	for _, eventInfo := range eventInfos {
		eventInfoDB := convertEventInfoToEventInfoDB(eventInfo, clusterName)
		eventInfoDBs = append(eventInfoDBs, eventInfoDB)
	}

	if err := db.SaveEventInfoBatch(eventInfoDBs); err != nil {
		log.Printf("Error saving event infos to database: %v", err)
		return nil, fmt.Errorf("failed to save events to database: %w", err)
	}

	return eventInfos, nil
}

func getRelatedResource(obj corev1.ObjectReference, clusterName string, config *utils.Config) string {
	clientset, _, err := utils.GetKubernetesClient(clusterName, config)
	if err != nil {
		return fmt.Sprintf("%s/%s", obj.Kind, obj.Name)
	}

	switch obj.Kind {
	case "Pod":
		return fmt.Sprintf("Pod/%s", obj.Name)
	case "ReplicaSet":
		rs, err := clientset.AppsV1().ReplicaSets(obj.Namespace).Get(context.TODO(), obj.Name, metav1.GetOptions{})
		if err == nil && len(rs.OwnerReferences) > 0 {
			return fmt.Sprintf("ReplicaSet/%s (Deployment: %s)", obj.Name, rs.OwnerReferences[0].Name)
		}
	case "Deployment":
		return fmt.Sprintf("Deployment/%s", obj.Name)
	case "StatefulSet":
		return fmt.Sprintf("StatefulSet/%s", obj.Name)
	case "Service":
		return fmt.Sprintf("Service/%s", obj.Name)
	case "Node":
		return fmt.Sprintf("Node/%s", obj.Name)
	}
	return fmt.Sprintf("%s/%s", obj.Kind, obj.Name)
}

var unknownTime = time.Unix(0, 0) // 1970-01-01 00:00:00 UTC

func convertEventInfoToEventInfoDB(eventInfo EventInfo, clusterName string) db.EventInfoDB {
	involvedObjectJSON, _ := json.Marshal(eventInfo.InvolvedObject)

	eventTime := eventInfo.Time.Time
	if eventTime.IsZero() {
		eventTime = unknownTime
	}

	return db.EventInfoDB{
		Cluster:         clusterName,
		Type:            eventInfo.Type,
		Message:         eventInfo.Message,
		Reason:          eventInfo.Reason,
		Time:            eventTime,
		InvolvedObject:  string(involvedObjectJSON),
		RelatedResource: eventInfo.RelatedResource,
		Timestamp:       time.Now(),
	}
}

func GetEventInfoFromDB(w http.ResponseWriter, r *http.Request) {
	query, err := utils.ParseTimeRangeQuery(r)
	if err != nil {
		http.Error(w, "Invalid query parameters", http.StatusBadRequest)
		return
	}

	// 获取集群名称参数
	clusterName := r.URL.Query().Get("cluster")

	var eventInfos []db.EventInfoDB
	if query.UseTimeRange {
		eventInfos, err = db.GetEventInfo(clusterName, 0, 0, query.StartTime, query.EndTime)
	} else {
		eventInfos, err = db.GetEventInfo(clusterName, query.Limit, query.Offset, time.Time{}, time.Time{})
	}

	if err != nil {
		http.Error(w, "Error retrieving event info from database", http.StatusInternalServerError)
		log.Printf("Error retrieving event info: %v", err)
		return
	}

	// 处理未知时间
	for i, event := range eventInfos {
		if event.Time.Equal(unknownTime) {
			eventInfos[i].Time = time.Time{} // 将未知时间转换回零值
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(eventInfos); err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
		log.Printf("Error encoding response: %v", err)
	}
}
