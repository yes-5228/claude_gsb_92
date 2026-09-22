// Package database 负责数据库连接、表结构迁移。
package database

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/drainage/desilting/internal/config"
	"github.com/drainage/desilting/internal/modules/acceptance"
	"github.com/drainage/desilting/internal/modules/cleaningrecord"
	"github.com/drainage/desilting/internal/modules/cleaningtask"
	"github.com/drainage/desilting/internal/modules/pipesegment"
	"github.com/drainage/desilting/internal/shared/date"
)

// Open 根据配置建立数据库连接。
//
// 生产与 docker compose 使用 PostgreSQL；本地开发或单元测试可以切到 SQLite，
// 两种驱动共用同一套 model 与查询代码。
func Open(cfg *config.Config) (*gorm.DB, error) {
	dialector, err := dialectorFor(cfg)
	if err != nil {
		return nil, err
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger:         gormlogger.Default.LogMode(gormlogger.Warn),
		TranslateError: true,
		NowFunc:        func() time.Time { return time.Now().In(time.Local) },
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(time.Hour)

	return db, nil
}

func dialectorFor(cfg *config.Config) (gorm.Dialector, error) {
	switch cfg.DBDriver {
	case config.DriverSQLite:
		if dir := filepath.Dir(cfg.DBPath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("创建 SQLite 数据目录失败: %w", err)
			}
		}
		return sqlite.Open(cfg.DBPath), nil
	case config.DriverPostgres:
		return postgres.Open(cfg.PostgresDSN()), nil
	default:
		return nil, fmt.Errorf("不支持的数据库驱动 %q，可选值为 %s / %s",
			cfg.DBDriver, config.DriverPostgres, config.DriverSQLite)
	}
}

// Migrate 建立/更新所有业务表。
//
// 表之间的引用关系由应用层在 service 中校验，因此这里不建外键约束，
// 便于后续按模块拆库时平滑迁移。
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&pipesegment.PipeSegment{},
		&cleaningtask.CleaningTask{},
		&cleaningrecord.CleaningRecord{},
		&acceptance.AcceptanceRecord{},
		&pipesegment.CleaningLedger{},
	); err != nil {
		return err
	}
	return backfillCleaningLedger(db)
}

// backfillCleaningLedger 为台账流水表上线前已经验收合格的任务补齐初始流水。
//
// 只补当前仍处于已验收状态、且尚无有效流水的任务；管段当前统计保持不变。
func backfillCleaningLedger(db *gorm.DB) error {
	type acceptedTask struct {
		ID              uint
		PipeSegmentID   uint
		AcceptanceID    uint
		AcceptedAt      date.Date
		LatestCleanedAt date.Date
	}
	var tasks []acceptedTask
	passAcceptance := db.Table("acceptance_records AS latest").
		Select("MAX(latest.id)").
		Where("latest.task_id = t.id AND latest.result = ?", acceptance.ResultPass)
	err := db.Table("cleaning_tasks AS t").
		Select(`t.id, t.pipe_segment_id, a.id AS acceptance_id, a.accepted_at,
			COALESCE(MAX(r.cleaned_at), a.accepted_at) AS latest_cleaned_at`).
		Joins("INNER JOIN acceptance_records AS a ON a.id = (?)", passAcceptance).
		Joins("LEFT JOIN cleaning_records AS r ON r.task_id = t.id").
		Where("t.status = ?", cleaningtask.StatusAccepted).
		Group("t.id, t.pipe_segment_id, a.id, a.accepted_at").
		Scan(&tasks).Error
	if err != nil {
		return err
	}

	ledgers := make([]pipesegment.CleaningLedger, 0, len(tasks))
	for _, task := range tasks {
		var count int64
		if err := db.Model(&pipesegment.CleaningLedger{}).
			Where("task_id = ? AND event_type = ? AND reversed_acceptance_id IS NULL", task.ID, pipesegment.LedgerEntryAccepted).
			Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		acceptanceID := task.AcceptanceID
		ledgers = append(ledgers, pipesegment.CleaningLedger{
			SegmentID:          task.PipeSegmentID,
			TaskID:             task.ID,
			SourceAcceptanceID: &acceptanceID,
			CleanedAt:          task.LatestCleanedAt,
			AcceptedAt:         task.AcceptedAt,
			EventType:          pipesegment.LedgerEntryAccepted,
			Delta:              1,
		})
	}
	if len(ledgers) == 0 {
		return nil
	}
	return db.Create(&ledgers).Error
}
