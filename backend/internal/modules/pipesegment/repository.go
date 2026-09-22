package pipesegment

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/drainage/desilting/internal/shared/date"
	"github.com/drainage/desilting/internal/shared/refx"
)

// ErrNotFound 管段不存在。
var ErrNotFound = errors.New("管段不存在")

// Repository 管段台账数据访问。
type Repository struct {
	db *gorm.DB
}

// NewRepository 构造仓储。
func NewRepository(db *gorm.DB) *Repository {
	return &Repository{db: db}
}

// DB 暴露底层连接，供 service 做跨模块统计。
func (r *Repository) DB() *gorm.DB {
	return r.db
}

// Create 新增管段。
func (r *Repository) Create(ctx context.Context, segment *PipeSegment) error {
	return r.db.WithContext(ctx).Create(segment).Error
}

// Save 保存管段全部字段。
func (r *Repository) Save(ctx context.Context, segment *PipeSegment) error {
	return r.db.WithContext(ctx).Save(segment).Error
}

// Delete 物理删除管段。
func (r *Repository) Delete(ctx context.Context, id uint) error {
	result := r.db.WithContext(ctx).Delete(&PipeSegment{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// FindByID 按主键查询。
func (r *Repository) FindByID(ctx context.Context, id uint) (*PipeSegment, error) {
	var segment PipeSegment
	err := r.db.WithContext(ctx).First(&segment, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &segment, nil
}

// ExistsByCode 判断管段编号是否已被占用（excludeID 用于修改时排除自身）。
func (r *Repository) ExistsByCode(ctx context.Context, code string, excludeID uint) (bool, error) {
	query := r.db.WithContext(ctx).Model(&PipeSegment{}).Where("code = ?", code)
	if excludeID > 0 {
		query = query.Where("id <> ?", excludeID)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// List 分页查询管段台账。
func (r *Repository) List(ctx context.Context, query ListQuery) ([]PipeSegment, int64, error) {
	query.Page.Normalize()
	var total int64
	if err := r.filtered(ctx, query).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	segments := make([]PipeSegment, 0)
	err := r.filtered(ctx, query).
		Order("district ASC, code ASC").
		Offset(query.Page.Offset()).
		Limit(query.Page.PageSize).
		Find(&segments).Error
	if err != nil {
		return nil, 0, err
	}
	return segments, total, nil
}

func (r *Repository) filtered(ctx context.Context, query ListQuery) *gorm.DB {
	tx := r.db.WithContext(ctx).Model(&PipeSegment{})
	if keyword := strings.ToLower(strings.TrimSpace(query.Keyword)); keyword != "" {
		like := "%" + keyword + "%"
		tx = tx.Where(
			"LOWER(code) LIKE ? OR LOWER(name) LIKE ? OR LOWER(road_name) LIKE ? OR LOWER(start_manhole) LIKE ? OR LOWER(end_manhole) LIKE ?",
			like, like, like, like, like,
		)
	}
	if query.District != "" {
		tx = tx.Where("district = ?", query.District)
	}
	if query.PipeType != "" {
		tx = tx.Where("pipe_type = ?", query.PipeType)
	}
	if query.Status != "" {
		tx = tx.Where("status = ?", query.Status)
	}
	return tx
}

// Search 按关键字搜索管段，用于下拉选择。
func (r *Repository) Search(ctx context.Context, keyword string, limit int) ([]Brief, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	tx := r.db.WithContext(ctx).Model(&PipeSegment{}).
		Select("id, code, name, district, road_name")
	if trimmed := strings.ToLower(strings.TrimSpace(keyword)); trimmed != "" {
		like := "%" + trimmed + "%"
		tx = tx.Where("LOWER(code) LIKE ? OR LOWER(name) LIKE ?", like, like)
	}
	items := make([]Brief, 0, limit)
	err := tx.Order("code ASC").Limit(limit).Scan(&items).Error
	return items, err
}

// BriefsByIDs 批量查询管段精简信息，避免列表接口出现 N+1 查询。
func (r *Repository) BriefsByIDs(ctx context.Context, ids []uint) (map[uint]Brief, error) {
	result := make(map[uint]Brief, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	items := make([]Brief, 0, len(ids))
	err := r.db.WithContext(ctx).Model(&PipeSegment{}).
		Select("id, code, name, district, road_name").
		Where("id IN ?", ids).
		Scan(&items).Error
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		result[item.ID] = item
	}
	return result, nil
}

// Districts 返回全部已使用的片区名称。
func (r *Repository) Districts(ctx context.Context) ([]string, error) {
	districts := make([]string, 0)
	err := r.db.WithContext(ctx).Model(&PipeSegment{}).
		Distinct().
		Order("district ASC").
		Pluck("district", &districts).Error
	return districts, err
}

// CleaningLedgerEntry 记录一次验收合格对管段台账的影响。
type CleaningLedgerEntry struct {
	SegmentID    uint
	TaskID       uint
	AcceptanceID uint
	CleanedAt    date.Date
	AcceptedAt   date.Date
}

// ActiveCleaningLedger 查询任务当前仍有效的合格台账流水。
func (r *Repository) ActiveCleaningLedger(ctx context.Context, tx *gorm.DB, taskID uint) (*CleaningLedger, error) {
	db := r.db
	if tx != nil {
		db = tx
	}
	var entry CleaningLedger
	err := db.WithContext(ctx).
		Where("task_id = ? AND event_type = ? AND reversed_acceptance_id IS NULL", taskID, LedgerEntryAccepted).
		First(&entry).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

// AddCleaningLedger 验收合格时写入 +1 台账流水，并重算管段当前清淤统计。
func (r *Repository) AddCleaningLedger(ctx context.Context, tx *gorm.DB, entry CleaningLedgerEntry) error {
	db := r.db
	if tx != nil {
		db = tx
	}
	ledger := &CleaningLedger{
		SegmentID:          entry.SegmentID,
		TaskID:             entry.TaskID,
		SourceAcceptanceID: &entry.AcceptanceID,
		CleanedAt:          entry.CleanedAt,
		AcceptedAt:         entry.AcceptedAt,
		EventType:          LedgerEntryAccepted,
		Delta:              1,
	}
	if err := db.WithContext(ctx).Create(ledger).Error; err != nil {
		return err
	}
	return r.rebuildCleaningStats(ctx, db, entry.SegmentID, true)
}

// ReverseCleaningLedger 合格验收回退时追加 -1 冲销流水。
//
// 不更新原 +1 流水；统计时通过 reversed_acceptance_id 排除已冲销记录。
func (r *Repository) ReverseCleaningLedger(
	ctx context.Context,
	tx *gorm.DB,
	active *CleaningLedger,
	acceptanceID uint,
	reversedAt time.Time,
	reason string,
) error {
	db := r.db
	if tx != nil {
		db = tx
	}
	reversedAcceptanceID := acceptanceID
	reversal := &CleaningLedger{
		SegmentID:            active.SegmentID,
		TaskID:               active.TaskID,
		SourceAcceptanceID:   active.SourceAcceptanceID,
		CleanedAt:            active.CleanedAt,
		AcceptedAt:           active.AcceptedAt,
		EventType:            LedgerEntryReversed,
		Delta:                -1,
		ReversedAcceptanceID: &reversedAcceptanceID,
		ReversalReason:       reason,
		ReversedAt:           &reversedAt,
	}
	if err := db.WithContext(ctx).Create(reversal).Error; err != nil {
		return err
	}
	return r.rebuildCleaningStats(ctx, db, active.SegmentID, false)
}

// rebuildCleaningStats 按仍有效的合格流水重算累计次数与最近清淤时间。
//
// 只依据未冲销的合格流水更新当前台账；冲销流水保留原作业月份，不回改历史月份。
func (r *Repository) rebuildCleaningStats(ctx context.Context, db *gorm.DB, segmentID uint, setNormal bool) error {
	type stats struct {
		Total         int64
		LastCleanedAt *date.Date
	}
	var result stats
	err := db.WithContext(ctx).Model(&CleaningLedger{}).
		Select("COUNT(*) AS total, MAX(cleaned_at) AS last_cleaned_at").
		Where("segment_id = ? AND event_type = ? AND reversed_acceptance_id IS NULL", segmentID, LedgerEntryAccepted).
		Scan(&result).Error
	if err != nil {
		return err
	}

	updates := map[string]any{
		"cleaned_times":   result.Total,
		"updated_at":      time.Now(),
		"last_cleaned_at": result.LastCleanedAt,
	}
	if setNormal {
		updates["status"] = StatusNormal
	}
	query := db.WithContext(ctx).Model(&PipeSegment{}).
		Where("id = ? AND (status <> ? OR ?)", segmentID, StatusBlocked, setNormal)
	return query.Updates(updates).Error
}

// TaskStats 汇总管段下的任务状态分布。
func (r *Repository) TaskStats(ctx context.Context, segmentID uint) (refx.TaskStats, error) {
	return refx.TaskStatsForSegment(ctx, r.db, segmentID)
}

// RecentTasks 查询管段最近的任务。
func (r *Repository) RecentTasks(ctx context.Context, segmentID uint, limit int) ([]refx.TaskRef, error) {
	return refx.RecentTasksForSegment(ctx, r.db, segmentID, limit)
}

// History 查询管段的清淤履历。
func (r *Repository) History(ctx context.Context, segmentID uint) ([]refx.HistoryItem, error) {
	return refx.HistoryForSegment(ctx, r.db, segmentID)
}
