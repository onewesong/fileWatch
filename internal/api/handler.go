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
			"title":          "文件访问监控",
			"monitoring":     monitoringActive,
			"includePattern": monitor.GetIncludePattern(),
			"excludePattern": monitor.GetExcludePattern(),
			"processPattern": monitor.GetProcessPattern(),
			"storeStats":     database.GetStoreStats(),
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

		// 获取内存存储统计信息
		api.GET("/store/stats", getStoreStats)

		// 设置内存存储的最大记录数
		api.POST("/store/max-records", setMaxRecords)
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

	// 解析请求体，获取通配符参数
	var request struct {
		IncludePattern string `json:"includePattern"` // 包含目录通配符
		ExcludePattern string `json:"excludePattern"` // 排除目录通配符
		ProcessPattern string `json:"processPattern"` // 进程通配符
		// 以下参数为了向后兼容保留
		IncludeRegex string `json:"includeRegex"` // 旧的包含目录正则表达式
		ExcludeRegex string `json:"excludeRegex"` // 旧的排除目录正则表达式
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		// 如果解析失败也不要报错，视为没有提供参数
		request.IncludePattern = ""
		request.ExcludePattern = ""
		request.ProcessPattern = ""
	}

	// 如果新参数为空，尝试使用旧参数
	if request.IncludePattern == "" && request.IncludeRegex != "" {
		request.IncludePattern = request.IncludeRegex
	}
	if request.ExcludePattern == "" && request.ExcludeRegex != "" {
		request.ExcludePattern = request.ExcludeRegex
	}

	// 创建一个通道用于停止监控
	doneChan = make(chan bool)
	monitoringActive = true

	// 使用通配符匹配模式启动监控
	go monitor.StartMonitoringWithWildcards(doneChan, request.IncludePattern, request.ExcludePattern, request.ProcessPattern)

	c.JSON(http.StatusOK, gin.H{
		"message":        "已启动文件系统监控",
		"command":        monitor.GetFSUsageCommand(),
		"includePattern": request.IncludePattern,
		"excludePattern": request.ExcludePattern,
		"processPattern": request.ProcessPattern,
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

	// 重置所有过滤条件
	monitor.ResetPathPrefix()
	monitor.ResetExcludeRegex()
	monitor.ResetProcessPattern()

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

// getStoreStats 获取内存存储的统计信息
func getStoreStats(c *gin.Context) {
	stats := database.GetStoreStats()
	c.JSON(http.StatusOK, stats)
}

// setMaxRecords 设置内存存储的最大记录数
func setMaxRecords(c *gin.Context) {
	var request struct {
		MaxRecords int `json:"maxRecords" binding:"required"`
	}

	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请提供有效的maxRecords参数"})
		return
	}

	if request.MaxRecords <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "maxRecords必须大于0"})
		return
	}

	database.SetMaxRecords(request.MaxRecords)

	c.JSON(http.StatusOK, gin.H{
		"message": "成功设置最大记录数",
		"stats":   database.GetStoreStats(),
	})
}
