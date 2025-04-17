package monitor

import (
	"bufio"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mine/fileWatch/internal/database"
)

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

	// 使用扫描器读取命令输出
	scanner := bufio.NewScanner(stdout)
	go func() {
		for scanner.Scan() {
			line := scanner.Text()

			// 解析fs_usage输出行
			if access := parseFsUsageLine(line); access != nil {
				// 存储到数据库
				if err := database.AddFileAccess(*access); err != nil {
					log.Printf("存储文件访问记录失败: %v", err)
				}
			}
		}
	}()

	// 等待停止信号
	<-doneChan
	if err := cmd.Process.Kill(); err != nil {
		log.Printf("停止fs_usage命令失败: %v", err)
	}
	log.Println("已停止监控文件系统访问")
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
		"unlink":         true,
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
	// 快速检查常见路径

	// 始终忽略的路径
	if strings.HasPrefix(path, "/dev/") {
		return false
	}

	// 默认保留
	return true
}

// GetFSUsageCommand 返回适合用户执行的fs_usage命令
func GetFSUsageCommand() string {
	return "sudo fs_usage -w -f filesystem"
}
