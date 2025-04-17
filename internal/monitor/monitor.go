package monitor

import (
	"bufio"
	"log"
	"os/exec"
	"path/filepath"
	"regexp"
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

// 全局变量，用于存储包含目录的通配符模式
var includePattern string

// 全局变量，用于存储排除目录的通配符模式
var excludePattern string

// 全局变量，用于存储包含进程的通配符模式
var processPattern string

// 全局变量，用于存储监控目录的正则表达式
var includeRegexPattern string
var includeRegex *regexp.Regexp

// 全局变量，用于存储排除目录的正则表达式
var excludeRegexPattern string
var excludeRegex *regexp.Regexp

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

	// 根据进程名过滤
	if !shouldTrackProcess(processName) {
		return nil
	}

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
	// 如果设置了包含通配符，则只记录匹配的路径
	if includePattern != "" && !matchWildcard(path, includePattern) {
		return false
	}

	// 如果设置了排除通配符，则排除匹配的路径
	if excludePattern != "" && matchWildcard(path, excludePattern) {
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

// shouldTrackProcess 判断是否应该记录该进程的访问
func shouldTrackProcess(processName string) bool {
	// 如果没有设置进程通配符，则记录所有进程
	if processPattern == "" {
		return true
	}

	// 否则只记录匹配通配符的进程
	return matchWildcard(processName, processPattern)
}

// matchWildcard 判断路径是否匹配通配符模式
// 支持 * 匹配任意字符序列, ? 匹配单个字符
func matchWildcard(path, pattern string) bool {
	// 使用Go标准库的filepath.Match函数进行通配符匹配
	matched, err := filepath.Match(pattern, path)
	if err != nil {
		// 如果模式无效，记录错误并返回false
		log.Printf("通配符模式 '%s' 无效: %v", pattern, err)
		return false
	}

	// 如果直接匹配成功，返回true
	if matched {
		return true
	}

	// filepath.Match只支持匹配单个路径段，但我们需要匹配整个路径
	// 如果pattern包含**/，表示匹配任意层级目录
	if strings.Contains(pattern, "**/") {
		parts := strings.Split(pattern, "**/")
		if len(parts) == 2 {
			prefix := parts[0]
			suffix := parts[1]

			// 检查路径是否以prefix开头
			if prefix == "" || strings.HasPrefix(path, prefix) {
				// 遍历所有可能的目录层级
				restPath := path
				if prefix != "" {
					restPath = path[len(prefix):]
				}

				// 处理路径中的所有子目录
				dirs := strings.Split(restPath, "/")
				for i := 0; i < len(dirs); i++ {
					subPath := strings.Join(dirs[i:], "/")
					matched, err := filepath.Match(suffix, subPath)
					if err == nil && matched {
						return true
					}
				}
			}
		}
	}

	// 如果模式以*开头或结尾，我们需要进行部分匹配
	if strings.HasPrefix(pattern, "*") && strings.HasSuffix(path, pattern[1:]) {
		return true
	}

	if strings.HasSuffix(pattern, "*") && strings.HasPrefix(path, pattern[:len(pattern)-1]) {
		return true
	}

	// 多个通配符分段匹配
	if strings.Count(pattern, "*") > 1 {
		segments := strings.Split(pattern, "*")
		if segments[0] != "" && !strings.HasPrefix(path, segments[0]) {
			return false
		}

		if segments[len(segments)-1] != "" && !strings.HasSuffix(path, segments[len(segments)-1]) {
			return false
		}

		// 检查中间部分是否按顺序出现
		current := path
		for i := 0; i < len(segments); i++ {
			if segments[i] == "" {
				continue
			}

			index := strings.Index(current, segments[i])
			if index == -1 {
				return false
			}
			current = current[index+len(segments[i]):]
		}

		return true
	}

	return false
}

// GetFSUsageCommand 返回适合用户执行的fs_usage命令
func GetFSUsageCommand() string {
	return "sudo fs_usage -w -f filesystem"
}

// StartMonitoringWithPrefix 开始监控文件系统访问，支持指定目录前缀
func StartMonitoringWithPrefix(doneChan chan bool, pathPattern string) {
	// 旧版本的目录前缀功能，保留向后兼容
	currentPathPrefix = pathPattern

	// 如果提供了路径模式，则尝试设置为通配符
	if pathPattern != "" {
		// 自动将前缀路径转为通配符模式
		wildcardPattern := pathPattern + "*"
		SetIncludePattern(wildcardPattern)
	} else {
		// 如果没有提供路径，清除通配符
		ResetIncludePattern()
	}

	log.Printf("开始监控文件系统访问，目录匹配模式: %s", includePattern)

	// 调用原有监控函数
	StartMonitoring(doneChan)
}

// StartMonitoringWithWildcards 开始监控文件系统访问，支持指定通配符
func StartMonitoringWithWildcards(doneChan chan bool, includeWildcard string, excludeWildcard string, processWildcard string) {
	// 设置包含通配符
	if includeWildcard != "" {
		SetIncludePattern(includeWildcard)
	} else {
		ResetIncludePattern()
	}

	// 设置排除通配符
	if excludeWildcard != "" {
		SetExcludePattern(excludeWildcard)
	} else {
		ResetExcludePattern()
	}

	// 设置进程通配符
	if processWildcard != "" {
		SetProcessPattern(processWildcard)
	} else {
		ResetProcessPattern()
	}

	log.Printf("开始监控文件系统访问，包含路径通配符: %s, 排除路径通配符: %s, 进程通配符: %s",
		includePattern, excludePattern, processPattern)

	// 调用原有监控函数
	StartMonitoring(doneChan)
}

// SetIncludePattern 设置包含目录的通配符
func SetIncludePattern(pattern string) {
	if pattern == "" {
		ResetIncludePattern()
		return
	}

	includePattern = pattern
	log.Printf("已设置包含目录通配符: %s", pattern)
}

// GetIncludePattern 获取当前的包含目录通配符
func GetIncludePattern() string {
	return includePattern
}

// ResetIncludePattern 重置包含目录通配符
func ResetIncludePattern() {
	includePattern = ""
	log.Println("已重置包含目录通配符")
}

// SetExcludePattern 设置排除目录的通配符
func SetExcludePattern(pattern string) {
	if pattern == "" {
		ResetExcludePattern()
		return
	}

	excludePattern = pattern
	log.Printf("已设置排除目录通配符: %s", pattern)
}

// GetExcludePattern 获取当前的排除目录通配符
func GetExcludePattern() string {
	return excludePattern
}

// ResetPathPrefix 重置监控目录前缀
func ResetPathPrefix() {
	currentPathPrefix = ""
	log.Println("已重置监控目录前缀")
	// 同时重置包含目录通配符
	ResetIncludePattern()
}

// ResetExcludePattern 重置排除目录通配符
func ResetExcludePattern() {
	excludePattern = ""
	log.Println("已重置排除目录通配符")
}

// GetCurrentPathPrefix 获取当前监控的目录前缀
func GetCurrentPathPrefix() string {
	return currentPathPrefix
}

// 以下函数为了保持向后兼容性，但内部实现已改为通配符

// SetIncludeRegex 设置包含目录的正则表达式（兼容旧API）
func SetIncludeRegex(pattern string) error {
	log.Println("警告: SetIncludeRegex 已弃用，请使用 SetIncludePattern")
	SetIncludePattern(pattern)
	return nil
}

// GetIncludeRegex 获取当前的包含目录正则表达式（兼容旧API）
func GetIncludeRegex() string {
	return GetIncludePattern()
}

// ResetIncludeRegex 重置包含目录正则表达式（兼容旧API）
func ResetIncludeRegex() {
	ResetIncludePattern()
}

// SetExcludeRegex 设置排除目录的正则表达式（兼容旧API）
func SetExcludeRegex(pattern string) error {
	log.Println("警告: SetExcludeRegex 已弃用，请使用 SetExcludePattern")
	SetExcludePattern(pattern)
	return nil
}

// GetExcludeRegex 获取当前的排除目录正则表达式（兼容旧API）
func GetExcludeRegex() string {
	return GetExcludePattern()
}

// ResetExcludeRegex 重置排除目录正则表达式（兼容旧API）
func ResetExcludeRegex() {
	ResetExcludePattern()
}

// StartMonitoringWithRegex 开始监控文件系统访问（兼容旧API）
func StartMonitoringWithRegex(doneChan chan bool, includePattern string, excludePattern string) {
	log.Println("警告: StartMonitoringWithRegex 已弃用，请使用 StartMonitoringWithWildcards")
	StartMonitoringWithWildcards(doneChan, includePattern, excludePattern, "")
}

// SetProcessPattern 设置包含进程的通配符
func SetProcessPattern(pattern string) {
	if pattern == "" {
		ResetProcessPattern()
		return
	}

	processPattern = pattern
	log.Printf("已设置包含进程通配符: %s", pattern)
}

// GetProcessPattern 获取当前的包含进程通配符
func GetProcessPattern() string {
	return processPattern
}

// ResetProcessPattern 重置包含进程通配符
func ResetProcessPattern() {
	processPattern = ""
	log.Println("已重置包含进程通配符")
}
