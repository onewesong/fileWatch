package database

import (
	"log"
	"sort"
	"strings"
	"time"
)

// 全局内存存储实例
var Store *MemoryStore

// InitDB 初始化内存数据存储
func InitDB(_ string) error {
	// 忽略数据库路径参数，使用内存存储
	Store = NewMemoryStore(100000) // 默认存储10万条记录
	log.Println("内存数据存储初始化成功")
	return nil
}

// AddFileAccess 添加文件访问记录
func AddFileAccess(access FileAccess) error {
	Store.mu.Lock()
	defer Store.mu.Unlock()

	// 设置ID和创建时间
	access.ID = Store.currentID
	Store.currentID++
	access.CreatedAt = time.Now()

	// 添加记录
	Store.accesses = append(Store.accesses, access)

	// 检查是否超过最大记录数
	if len(Store.accesses) > Store.maxRecords {
		// 删除最旧的20%的记录
		removeCount := Store.maxRecords / 5
		Store.accesses = Store.accesses[removeCount:]
	}

	return nil
}

// AddFileAccessBatch 批量添加文件访问记录
func AddFileAccessBatch(accesses []FileAccess) error {
	if len(accesses) == 0 {
		return nil
	}

	Store.mu.Lock()
	defer Store.mu.Unlock()

	// 为所有记录设置ID和创建时间
	now := time.Now()
	for i := range accesses {
		accesses[i].ID = Store.currentID
		Store.currentID++
		accesses[i].CreatedAt = now
	}

	// 批量添加记录
	Store.accesses = append(Store.accesses, accesses...)

	// 检查是否超过最大记录数
	if len(Store.accesses) > Store.maxRecords {
		// 删除最旧的20%的记录
		removeCount := Store.maxRecords / 5
		Store.accesses = Store.accesses[removeCount:]
	}

	return nil
}

// GetFileAccessList 获取最近的文件访问记录
func GetFileAccessList(limit int) ([]FileAccess, error) {
	Store.mu.RLock()
	defer Store.mu.RUnlock()

	result := make([]FileAccess, 0, limit)

	// 复制最新的记录
	totalRecords := len(Store.accesses)
	startIdx := totalRecords - limit
	if startIdx < 0 {
		startIdx = 0
	}

	// 按时间降序返回
	for i := totalRecords - 1; i >= startIdx; i-- {
		result = append(result, Store.accesses[i])
	}

	return result, nil
}

// GetAccessCountByProcess 获取各进程访问文件的次数统计
func GetAccessCountByProcess() ([]FileAccessSummary, error) {
	Store.mu.RLock()
	defer Store.mu.RUnlock()

	// 使用map统计每个进程的访问次数
	countMap := make(map[string]int)
	for _, access := range Store.accesses {
		countMap[access.ProcessName]++
	}

	// 转换为切片并排序
	result := make([]FileAccessSummary, 0, len(countMap))
	for processName, count := range countMap {
		result = append(result, FileAccessSummary{
			ProcessName: processName,
			Count:       count,
		})
	}

	// 按访问次数降序排序
	sort.Slice(result, func(i, j int) bool {
		return result[i].Count > result[j].Count
	})

	return result, nil
}

// GetRecentAccessByTimeRange 获取指定时间范围内的访问记录
func GetRecentAccessByTimeRange(start, end time.Time) ([]FileAccess, error) {
	Store.mu.RLock()
	defer Store.mu.RUnlock()

	var result []FileAccess

	// 筛选时间范围内的记录
	for i := len(Store.accesses) - 1; i >= 0; i-- {
		access := Store.accesses[i]
		if access.Timestamp.After(start) && access.Timestamp.Before(end) {
			result = append(result, access)
		}
	}

	return result, nil
}

// GetAccessByProcessName 获取指定进程的文件访问记录
func GetAccessByProcessName(processName string, limit int) ([]FileAccess, error) {
	Store.mu.RLock()
	defer Store.mu.RUnlock()

	result := make([]FileAccess, 0, limit)
	count := 0

	// 从最新记录开始，筛选指定进程名的记录
	for i := len(Store.accesses) - 1; i >= 0 && count < limit; i-- {
		if Store.accesses[i].ProcessName == processName {
			result = append(result, Store.accesses[i])
			count++
		}
	}

	return result, nil
}

// GetAccessByPathPrefix 获取指定路径前缀的文件访问记录
func GetAccessByPathPrefix(pathPrefix string, limit int) ([]FileAccess, error) {
	Store.mu.RLock()
	defer Store.mu.RUnlock()

	result := make([]FileAccess, 0, limit)
	count := 0

	// 从最新记录开始，筛选匹配路径前缀的记录
	for i := len(Store.accesses) - 1; i >= 0 && count < limit; i-- {
		if strings.HasPrefix(Store.accesses[i].FilePath, pathPrefix) {
			result = append(result, Store.accesses[i])
			count++
		}
	}

	return result, nil
}

// SetMaxRecords 设置存储的最大记录数
func SetMaxRecords(maxRecords int) {
	if maxRecords <= 0 {
		return
	}

	Store.mu.Lock()
	defer Store.mu.Unlock()

	Store.maxRecords = maxRecords

	// 如果当前记录数超过新的最大值，则裁剪
	if len(Store.accesses) > maxRecords {
		// 保留最新的记录
		Store.accesses = Store.accesses[len(Store.accesses)-maxRecords:]
	}
}

// GetStoreStats 获取内存存储的统计信息
func GetStoreStats() map[string]interface{} {
	Store.mu.RLock()
	defer Store.mu.RUnlock()

	return map[string]interface{}{
		"current_records": len(Store.accesses),
		"max_records":     Store.maxRecords,
		"next_id":         Store.currentID,
	}
}
