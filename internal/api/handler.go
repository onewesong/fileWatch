package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mine/fileWatch/internal/database"
	"github.com/mine/fileWatch/internal/monitor"
)

var monitoringActive bool
var doneChan chan bool

// InitRouter 初始化路由
func InitRouter() *gin.Engine {
	r := gin.Default()

	// 静态文件服务
	r.Static("/static", "./static")
	r.LoadHTMLGlob("templates/*.tmpl")

	// 主页路由
	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.tmpl", gin.H{
			"title":      "文件访问监控",
			"monitoring": monitoringActive,
			"pathPrefix": monitor.GetCurrentPathPrefix(),
		})
	})

	// API路由组
	api := r.Group("/api")
	{
		// 获取最近的访问记录
		api.GET("/recent", getRecentAccess)

		// 获取按进程分组的统计数据
		api.GET("/summary", getAccessSummary)

		// 启动监控
		api.POST("/monitor/start", startMonitoring)

		// 停止监控
		api.POST("/monitor/stop", stopMonitoring)

		// 获取按时间范围过滤的访问记录
		api.GET("/time-range", getAccessByTimeRange)

		// 获取指定进程的文件访问记录
		api.GET("/process-files", getProcessFiles)

		// 获取按文件路径前缀筛选的访问记录
		api.GET("/path-files", getFilesByPathPrefix)
	}

	return r
}

// getRecentAccess 获取最近的文件访问记录
func getRecentAccess(c *gin.Context) {
	limit := 100 // 默认限制为100条记录
	accesses, err := database.GetFileAccessList(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, accesses)
}

// getAccessSummary 获取按进程分组的访问统计
func getAccessSummary(c *gin.Context) {
	summary, err := database.GetAccessCountByProcess()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, summary)
}

// startMonitoring 启动文件系统监控
func startMonitoring(c *gin.Context) {
	if monitoringActive {
		c.JSON(http.StatusBadRequest, gin.H{"error": "监控已经在运行中"})
		return
	}

	// 解析请求体，获取目录前缀
	var request struct {
		PathPrefix string `json:"pathPrefix"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		// 如果解析失败也不要报错，视为没有提供前缀
		request.PathPrefix = ""
	}

	// 创建一个通道用于停止监控
	doneChan = make(chan bool)
	monitoringActive = true

	// 启动监控服务，传递目录前缀
	go monitor.StartMonitoringWithPrefix(doneChan, request.PathPrefix)

	c.JSON(http.StatusOK, gin.H{
		"message":    "已启动文件系统监控",
		"command":    monitor.GetFSUsageCommand(),
		"pathPrefix": request.PathPrefix,
	})
}

// stopMonitoring 停止文件系统监控
func stopMonitoring(c *gin.Context) {
	if !monitoringActive {
		c.JSON(http.StatusBadRequest, gin.H{"error": "监控未在运行"})
		return
	}

	// 发送停止信号
	doneChan <- true
	monitoringActive = false

	// 重置监控目录前缀
	monitor.ResetPathPrefix()

	c.JSON(http.StatusOK, gin.H{"message": "已停止文件系统监控"})
}

// getAccessByTimeRange 获取指定时间范围内的访问记录
func getAccessByTimeRange(c *gin.Context) {
	// 默认值为过去24小时
	endTime := time.Now()
	startTime := endTime.Add(-24 * time.Hour)

	// 从查询参数中解析时间范围
	startParam := c.Query("start")
	endParam := c.Query("end")

	if startParam != "" {
		if t, err := time.Parse(time.RFC3339, startParam); err == nil {
			startTime = t
		}
	}

	if endParam != "" {
		if t, err := time.Parse(time.RFC3339, endParam); err == nil {
			endTime = t
		}
	}

	// 获取记录
	accesses, err := database.GetRecentAccessByTimeRange(startTime, endTime)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, accesses)
}

// getProcessFiles 获取指定进程的文件访问记录
func getProcessFiles(c *gin.Context) {
	processName := c.Query("process")
	if processName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少进程名称参数"})
		return
	}

	limit := 100 // 默认限制为100条记录
	limitParam := c.Query("limit")
	if limitParam != "" {
		if n, err := strconv.Atoi(limitParam); err == nil && n > 0 {
			limit = n
		}
	}

	accesses, err := database.GetAccessByProcessName(processName, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, accesses)
}

// getFilesByPathPrefix 获取指定路径前缀的文件访问记录
func getFilesByPathPrefix(c *gin.Context) {
	pathPrefix := c.Query("prefix")
	if pathPrefix == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少路径前缀参数"})
		return
	}

	limit := 100 // 默认限制为100条记录
	limitParam := c.Query("limit")
	if limitParam != "" {
		if n, err := strconv.Atoi(limitParam); err == nil && n > 0 {
			limit = n
		}
	}

	accesses, err := database.GetAccessByPathPrefix(pathPrefix, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, accesses)
}
