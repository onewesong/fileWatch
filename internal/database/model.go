package database

import (
	"time"

	"gorm.io/gorm"
)

// FileAccess 表示文件访问记录
type FileAccess struct {
	gorm.Model
	Timestamp   time.Time `json:"timestamp"`
	ProcessName string    `json:"process_name"`
	PID         int       `json:"pid"`
	FilePath    string    `json:"file_path"` // 可能为空，因fs_usage输出格式不总是包含文件路径
	Operation   string    `json:"operation"` // 操作类型，如读取或写入
}

// FileAccessSummary 表示文件访问统计信息
type FileAccessSummary struct {
	ProcessName string `json:"process_name"`
	Count       int    `json:"count"`
}
