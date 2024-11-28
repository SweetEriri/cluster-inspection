package main

import (
	"cluster-inspection/db"
	"cluster-inspection/handlers"
	"cluster-inspection/report"
	"cluster-inspection/utils"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	// 加载配置
	config, err := utils.LoadConfig("config.yaml")
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	// 构建 DSN
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		config.Database.User, url.QueryEscape(config.Database.Password),
		config.Database.Host, config.Database.Port, config.Database.DBName)

	// 初始化数据库连接
	err = db.InitDB(dsn)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// 初始化报告处理器
	reportHandler, err := report.NewReportHandler(config)
	if err != nil {
		log.Fatalf("Error creating report handler: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	// 创建一个 WaitGroup 来等待所有 goroutine 完成
	var wg sync.WaitGroup

	// 启动定时任务
	wg.Add(1)
	go func() {
		defer wg.Done()
		startPeriodicDataCollection(reportHandler, ctx)
	}()

	// 设置路由（直接获取）
	http.HandleFunc("/api/report/nodes", reportHandler.GetNodeReport)
	http.HandleFunc("/api/report/nodes/", reportHandler.GetNodeReport)
	http.HandleFunc("/api/report/events", reportHandler.GetEventReport)
	http.HandleFunc("/api/report/events/", reportHandler.GetEventReport)
	http.HandleFunc("/api/report/pods", reportHandler.GetPodReport)
	http.HandleFunc("/api/report/pods/", reportHandler.GetPodReport)
	http.HandleFunc("/api/report/full", reportHandler.GetFullReport)
	http.HandleFunc("/api/clusters", reportHandler.GetAvailableClusters)
	// 设置路由（从数据库获取）
	http.HandleFunc("/api/nodes", handlers.GetNodeInfoFromDB)
	http.HandleFunc("/api/events", handlers.GetEventInfoFromDB)
	http.HandleFunc("/api/pods", handlers.GetPodInfoFromDB)
	http.HandleFunc("/api/all", reportHandler.GetAllReports)
	//获取所有数据（慎用）
	http.HandleFunc("/api/collect", triggerDataCollection(reportHandler))
	//获取部署资源推荐
	http.HandleFunc("/api/deployment-resource-recommendations", handlers.GetDeploymentResourceRecommendations)

	// 创建一个通道来接收终止信号
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	// 在一个 goroutine 中启动 HTTP 服务器
	serverErrors := make(chan error, 1)
	go func() {
		log.Println("Server is running on http://localhost:8088")
		serverErrors <- http.ListenAndServe(":8088", nil)
	}()

	// 等待终止信号或服务器错误
	select {
	case <-stop:
		log.Println("Shutting down gracefully...")
		cancel()
	case err := <-serverErrors:
		log.Fatalf("Could not start server: %v", err)
	}

	wg.Wait()
	log.Println("Server stopped")
}

// startPeriodicDataCollection 开始采集操作
func startPeriodicDataCollection(reportHandler *report.ReportHandler, ctx context.Context) {
	log.Println("Starting periodic data collection setup...")

	// 计算当前时间到下一个整点的时间间隔
	now := time.Now()
	nextHour := now.Truncate(time.Hour).Add(time.Hour)
	waitDuration := nextHour.Sub(now)

	// 日志记录等待时间，直到下一个整点
	log.Printf("Waiting %v until next hour to collect data", waitDuration)

	// 创建一个定时器，用于等待直到下一个整点
	timer := time.NewTimer(waitDuration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		log.Println("kill collection func")
		return
	case <-timer.C:
		// 到达整点，执行数据收集
		log.Println("Starting hourly data collection...")
		collectData(reportHandler)
	}

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("kill collection func")
			return
		case <-ticker.C:
			// 到达整点，执行数据收集
			log.Println("Starting hourly data collection...")
			collectData(reportHandler)
		}
	}
}

func collectData(reportHandler *report.ReportHandler) {
	log.Println("Starting periodic data collection...")

	req, _ := http.NewRequest("GET", "/api/report/nodes", nil)

	rr := httptest.NewRecorder()

	// 调用 GetNodeReport 处理函数
	reportHandler.GetNodeReport(rr, req)

	if rr.Code != http.StatusOK {
		log.Printf("Error collecting node data: %s", rr.Body.String())
	} else {
		log.Println("Node data collected and saved successfully")
	}

	req, _ = http.NewRequest("GET", "/api/report/pods", nil)
	rr = httptest.NewRecorder()
	reportHandler.GetPodReport(rr, req)

	if rr.Code != http.StatusOK {
		log.Printf("Error collecting pod data: %s", rr.Body.String())
	} else {
		log.Println("Pod data collected and saved successfully")
	}

	req, _ = http.NewRequest("GET", "/api/report/events", nil)
	rr = httptest.NewRecorder()
	reportHandler.GetEventReport(rr, req)

	if rr.Code != http.StatusOK {
		log.Printf("Error collecting event data: %s", rr.Body.String())
	} else {
		log.Println("Event data collected and saved successfully")
	}

	log.Println("Periodic data collection completed.")
}

func triggerDataCollection(reportHandler *report.ReportHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Println("Manual data collection triggered")
		collectData(reportHandler)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Data collection completed"))
	}
}
