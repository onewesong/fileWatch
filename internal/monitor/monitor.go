package monitor

import (
	"bufio"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mine/fileWatch/internal/database"
)

// 批处理缓冲区大小
const (
	batchSize     = 100
	flushInterval = 5 * time.Second
	debounceTime  = 500 * time.Millisecond
)

// 用于去重的缓存结构
type accessKey struct {
	process   string
	filePath  string
	operation string
}

// 全局变量，用于存储当前监控的目录前缀
var currentPathPrefix string

// StartMonitoring 开始监控文件系统访问
func StartMonitoring(doneChan chan bool) {
	log.Println("开始监控文件系统访问...")

	// 执行fs_usage命令，增加-w参数以显示完整路径
	cmd := exec.Command("sudo", "fs_usage", "-w", "-f", "filesystem")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("创建管道失败: %v", err)
		return
	}

	if err := cmd.Start(); err != nil {
		log.Printf("启动fs_usage命令失败: %v", err)
		return
	}

	// 创建批处理缓冲区
	var (
		accessBuffer = make([]database.FileAccess, 0, batchSize)
		bufferMutex  sync.Mutex
		stopChan     = make(chan bool)
		// 用于去重的缓存
		recentAccesses = make(map[accessKey]time.Time)
		cacheMutex     sync.Mutex
	)

	// 定期清理去重缓存
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				cleanupAccessCache(&recentAccesses, &cacheMutex)
			case <-stopChan:
				return
			}
		}
	}()

	// 定期刷新数据到数据库
	go func() {
		ticker := time.NewTicker(flushInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				flushAccessBuffer(&accessBuffer, &bufferMutex)
			case <-stopChan:
				// 确保退出前刷新所有数据
				flushAccessBuffer(&accessBuffer, &bufferMutex)
				return
			}
		}
	}()

	// 使用扫描器读取命令输出
	scanner := bufio.NewScanner(stdout)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()

			// 解析fs_usage输出行
			if access := parseFsUsageLine(line); access != nil {
				// 检查去重缓存，避免短时间内记录同一文件的重复操作
				key := accessKey{
					process:   access.ProcessName,
					filePath:  access.FilePath,
					operation: access.Operation,
				}

				cacheMutex.Lock()
				lastTime, exists := recentAccesses[key]
				now := time.Now()

				// 如果相同操作在抖动时间内出现过，则跳过
				if exists && now.Sub(lastTime) < debounceTime {
					cacheMutex.Unlock()
					continue
				}

				// 更新缓存
				recentAccesses[key] = now
				cacheMutex.Unlock()

				// 添加到缓冲区
				bufferMutex.Lock()
				accessBuffer = append(accessBuffer, *access)

				// 如果达到批处理大小，则刷新到数据库
				if len(accessBuffer) >= batchSize {
					// 复制当前缓冲区并清空，然后解锁，以便继续收集数据
					currentBatch := make([]database.FileAccess, len(accessBuffer))
					copy(currentBatch, accessBuffer)
					accessBuffer = accessBuffer[:0]
					bufferMutex.Unlock()

					// 批量存储到数据库
					if err := database.AddFileAccessBatch(currentBatch); err != nil {
						log.Printf("批量存储文件访问记录失败: %v", err)
					}
				} else {
					bufferMutex.Unlock()
				}
			}
		}
	}()

	// 等待停止信号
	<-doneChan
	close(stopChan) // 通知刷新goroutine退出

	if err := cmd.Process.Kill(); err != nil {
		log.Printf("停止fs_usage命令失败: %v", err)
	}
	log.Println("已停止监控文件系统访问")
}

// cleanupAccessCache 清理过期的缓存条目
func cleanupAccessCache(cache *map[accessKey]time.Time, mutex *sync.Mutex) {
	mutex.Lock()
	defer mutex.Unlock()

	now := time.Now()
	for key, lastTime := range *cache {
		// 删除30秒前的缓存条目
		if now.Sub(lastTime) > 30*time.Second {
			delete(*cache, key)
		}
	}
}

// flushAccessBuffer 将缓冲区中的访问记录刷新到数据库
func flushAccessBuffer(buffer *[]database.FileAccess, mutex *sync.Mutex) {
	mutex.Lock()
	defer mutex.Unlock()

	if len(*buffer) == 0 {
		return
	}

	// 复制并清空缓冲区
	batch := make([]database.FileAccess, len(*buffer))
	copy(batch, *buffer)
	*buffer = (*buffer)[:0]

	// 批量存储到数据库
	if err := database.AddFileAccessBatch(batch); err != nil {
		log.Printf("批量存储文件访问记录失败: %v", err)
	}
}

// parseFsUsageLine 解析fs_usage命令的单行输出
func parseFsUsageLine(line string) *database.FileAccess {
	// 跳过空行、标题行和其他非数据行
	if !strings.Contains(line, "/") {
		return nil
	}

	// 将行分割成字段
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return nil // 至少需要时间戳、操作类型和进程信息
	}

	// 提取时间戳和操作类型
	operation := fields[1]

	// 只记录读写文件的操作
	if !isReadWriteOperation(operation) {
		return nil
	}

	// 提取进程信息（通常是最后一个字段）
	processInfo := fields[len(fields)-1]
	processName, pid := parseProcessInfo(processInfo)

	// 提取文件路径
	filePath := extractFilePathSimple(line, fields)
	if filePath == "" {
		return nil
	}

	// 检查是否需要跟踪这个文件
	if !shouldTrackFile(filePath) {
		return nil
	}

	// 创建文件访问记录
	return &database.FileAccess{
		Timestamp:   time.Now(),
		ProcessName: processName,
		PID:         pid,
		FilePath:    filePath,
		Operation:   operation,
	}
}

// parseProcessInfo 从进程信息字符串中提取进程名和PID
func parseProcessInfo(info string) (string, int) {
	processName := info
	pid := 0

	// 尝试解析进程名和PID (格式通常是 processName.PID)
	lastDot := strings.LastIndex(info, ".")
	if lastDot > 0 && lastDot < len(info)-1 {
		processName = info[:lastDot]
		pidStr := info[lastDot+1:]
		pid, _ = strconv.Atoi(pidStr)
		if pid == 0 {
			// 如果PID解析失败，使用整个字符串作为进程名
			processName = info
		}
	}

	return processName, pid
}

// extractFilePathSimple 使用简单的字符串方法从输出行中提取文件路径
func extractFilePathSimple(line string, fields []string) string {
	// 寻找以/开头的字段，这很可能是文件路径
	for _, field := range fields {
		if strings.HasPrefix(field, "/") {
			return field
		}
	}

	// 检查是否有截断的路径（如 ystem/Volumes/）
	for i, field := range fields {
		if i > 0 && (strings.Contains(field, "/Volumes/") ||
			strings.Contains(field, "Library/") ||
			strings.Contains(field, "/Users/")) {

			// 可能是截断的路径
			if strings.HasPrefix(field, "ystem/") {
				return "/S" + field
			} else if strings.HasPrefix(field, "olumes/") {
				return "/V" + field
			} else if strings.HasPrefix(field, "ibrary/") {
				return "/L" + field
			} else if strings.HasPrefix(field, "sers/") {
				return "/U" + field
			} else {
				// 其他情况，如果看起来像是路径的一部分，加上前缀
				return "/" + field
			}
		}
	}

	return ""
}

// isReadWriteOperation 判断操作是否为读写文件相关操作
func isReadWriteOperation(operation string) bool {
	// 定义读写相关的操作类型
	readWriteOperations := map[string]bool{
		"read":           true,
		"read_nocancel":  true,
		"write":          true,
		"write_nocancel": true,
		"pread":          true,
		"pwrite":         true,
		"readv":          true,
		"writev":         true,
		"open":           true,
		"open_nocancel":  true,
		"close":          true,
		"close_nocancel": true,
		"create":         true,
		"unlink":         false,
		"rename":         true,
		"truncate":       true,
		"ftruncate":      true,
		"fsync":          true,
		"fwrite":         true,
		"fread":          true,
	}

	return readWriteOperations[operation]
}

// shouldTrackFile 判断是否应该记录该文件的访问
func shouldTrackFile(path string) bool {
	// 如果设置了目录前缀，则只记录以该前缀开始的路径
	if currentPathPrefix != "" && !strings.HasPrefix(path, currentPathPrefix) {
		return false
	}

	// 忽略系统目录和临时文件
	ignoredPrefixes := []string{
		"/dev/",
		"/usr/share/",
		"/private/var/folders/",
		"/System/Library/",
		"/Library/Caches/",
		"/Library/Logs/",
		"/var/log/",
		"/var/db/",
		"/private/tmp/",
		"/tmp/",
		"/Library/Apple/",
		"/Library/PrivilegedHelperTools/",
		"/Applications/Xcode.app/Contents/",
	}

	for _, prefix := range ignoredPrefixes {
		if strings.HasPrefix(path, prefix) {
			return false
		}
	}

	// 忽略某些临时文件扩展名
	ignoredExtensions := []string{
		".tmp",
		".temp",
		".cache",
		".swap",
		".swp",
		".DS_Store",
		".localized",
		".git",
	}

	for _, ext := range ignoredExtensions {
		if strings.HasSuffix(path, ext) {
			return false
		}
	}

	// 默认保留
	return true
}

// GetFSUsageCommand 返回适合用户执行的fs_usage命令
func GetFSUsageCommand() string {
	return "sudo fs_usage -w -f filesystem"
}

// StartMonitoringWithPrefix 开始监控文件系统访问，支持指定目录前缀
func StartMonitoringWithPrefix(doneChan chan bool, pathPrefix string) {
	// 设置目录前缀
	currentPathPrefix = pathPrefix
	log.Printf("开始监控文件系统访问，目录前缀: %s", currentPathPrefix)

	// 调用原有监控函数
	StartMonitoring(doneChan)
}

// ResetPathPrefix 重置监控目录前缀
func ResetPathPrefix() {
	currentPathPrefix = ""
	log.Println("已重置监控目录前缀")
}

// GetCurrentPathPrefix 获取当前监控的目录前缀
func GetCurrentPathPrefix() string {
	return currentPathPrefix
}
