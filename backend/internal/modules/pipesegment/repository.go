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

// CleaningSummary 管段当前有效清淤成果，只统计已验收合格的任务。
type CleaningSummary struct {
	CleanedTimes  int
	LastCleanedAt *date.Date
}

// CleaningSummaryForSegment 重算单个管段的清淤台账。
//
// 一个任务即一批作业，无论经历多少次需整改/复验，最多贡献一次清淤次数；
// 最近清淤时间取该管段下所有已验收任务的最晚清淤记录日期，任务没有记录时退回验收日期。
// tx 为 nil 时使用仓储自身连接。
func (r *Repository) CleaningSummaryForSegment(ctx context.Context, tx *gorm.DB, segmentID uint) (CleaningSummary, error) {
	db := r.db
	if tx != nil {
		db = tx
	}

	var summary struct {
		Total         int
		LastCleanedAt *date.Date
	}
	err := db.WithContext(ctx).Table(refx.TableCleaningTasks+" AS t").
		Select(`COUNT(*) AS total, MAX(COALESCE(r.latest_cleaned_at, DATE(t.accepted_at))) AS last_cleaned_at`).
		Joins(`LEFT JOIN (
			SELECT task_id, MAX(cleaned_at) AS latest_cleaned_at
			FROM `+refx.TableCleaningRecords+`
			GROUP BY task_id
		) AS r ON r.task_id = t.id`).
		Where("t.pipe_segment_id = ? AND t.status = ?", segmentID, "accepted").
		Scan(&summary).Error
	if err != nil {
		return CleaningSummary{}, err
	}
	return CleaningSummary{CleanedTimes: summary.Total, LastCleanedAt: summary.LastCleanedAt}, nil
}

// ReplaceCleaningSummary 用当前有效合格任务重算并覆盖管段清淤统计。
//
// 重算后仍有合格作业时，管段运行状态同步为正常；回退到 0 次时保留人工维护的当前状态。
func (r *Repository) ReplaceCleaningSummary(
	ctx context.Context,
	tx *gorm.DB,
	segmentID uint,
	summary CleaningSummary,
) error {
	db := r.db
	if tx != nil {
		db = tx
	}
	result := db.WithContext(ctx).Model(&PipeSegment{}).
		Where("id = ?", segmentID).
		Updates(map[string]any{
			"cleaned_times":   summary.CleanedTimes,
			"last_cleaned_at": summary.LastCleanedAt,
			"status": gorm.Expr(
				"CASE WHEN ? > 0 THEN ? ELSE status END",
				summary.CleanedTimes, StatusNormal,
			),
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
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
