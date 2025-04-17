package database

import (
	"log"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

var DB *gorm.DB

// InitDB 初始化数据库连接
func InitDB(dbPath string) error {
	var err error
	DB, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return err
	}

	// 自动迁移数据库结构
	err = DB.AutoMigrate(&FileAccess{})
	if err != nil {
		return err
	}

	log.Println("数据库初始化成功")
	return nil
}

// AddFileAccess 添加文件访问记录
func AddFileAccess(access FileAccess) error {
	return DB.Create(&access).Error
}

// AddFileAccessBatch 批量添加文件访问记录
func AddFileAccessBatch(accesses []FileAccess) error {
	// 创建事务
	tx := DB.Begin()
	if tx.Error != nil {
		return tx.Error
	}

	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err := tx.CreateInBatches(accesses, 100).Error; err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit().Error
}

// GetFileAccessList 获取最近的文件访问记录
func GetFileAccessList(limit int) ([]FileAccess, error) {
	var accesses []FileAccess
	err := DB.Order("timestamp desc").Limit(limit).Find(&accesses).Error
	return accesses, err
}

// GetAccessCountByProcess 获取各进程访问文件的次数统计
func GetAccessCountByProcess() ([]FileAccessSummary, error) {
	var summary []FileAccessSummary
	err := DB.Model(&FileAccess{}).
		Select("process_name, count(*) as count").
		Group("process_name").
		Order("count desc").
		Find(&summary).Error
	return summary, err
}

// GetRecentAccessByTimeRange 获取指定时间范围内的访问记录
func GetRecentAccessByTimeRange(start, end time.Time) ([]FileAccess, error) {
	var accesses []FileAccess
	err := DB.Where("timestamp BETWEEN ? AND ?", start, end).
		Order("timestamp desc").
		Find(&accesses).Error
	return accesses, err
}

// GetAccessByProcessName 获取指定进程的文件访问记录
func GetAccessByProcessName(processName string, limit int) ([]FileAccess, error) {
	var accesses []FileAccess
	err := DB.Where("process_name = ?", processName).
		Order("timestamp desc").
		Limit(limit).
		Find(&accesses).Error
	return accesses, err
}

// GetAccessByPathPrefix 获取指定路径前缀的文件访问记录
func GetAccessByPathPrefix(pathPrefix string, limit int) ([]FileAccess, error) {
	var accesses []FileAccess
	err := DB.Where("file_path LIKE ?", pathPrefix+"%").
		Order("timestamp desc").
		Limit(limit).
		Find(&accesses).Error
	return accesses, err
}
