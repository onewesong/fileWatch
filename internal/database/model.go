package database

import (
	"sync"
	"time"
)

// FileAccess 表示文件访问记录
type FileAccess struct {
	ID          uint      `json:"id"`
	CreatedAt   time.Time `json:"created_at"`
	Timestamp   time.Time `json:"timestamp"`
	ProcessName string    `json:"process_name"`
	PID         int       `json:"pid"`
	FilePath    string    `json:"file_path"`
	Operation   string    `json:"operation"`
}

// FileAccessSummary 表示文件访问统计信息
type FileAccessSummary struct {
	ProcessName string `json:"process_name"`
	Count       int    `json:"count"`
}

// MemoryStore 内存存储结构
type MemoryStore struct {
	accesses   []FileAccess
	mu         sync.RWMutex
	maxRecords int
	currentID  uint
}

// NewMemoryStore 创建新的内存存储
func NewMemoryStore(maxRecords int) *MemoryStore {
	if maxRecords <= 0 {
		maxRecords = 10000 // 默认值
	}
	return &MemoryStore{
		accesses:   make([]FileAccess, 0, maxRecords/2),
		maxRecords: maxRecords,
		currentID:  1,
	}
}
