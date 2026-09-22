// Package pipesegment 管段台账模块：维护排水管网管段的基础档案。
package pipesegment

import (
	"time"

	"github.com/drainage/desilting/internal/shared/date"
)

// 管段运行状态。
const (
	StatusNormal    = "normal"    // 正常
	StatusAttention = "attention" // 需关注
	StatusBlocked   = "blocked"   // 已淤堵
)

// 管段类型。
const (
	TypeRainwater = "rainwater" // 雨水
	TypeSewage    = "sewage"    // 污水
	TypeCombined  = "combined"  // 合流
)

// 管段清淤台账流水类型。
const (
	LedgerEntryAccepted = "accepted" // 验收合格写入
	LedgerEntryReversed = "reversed" // 合格结论回退
)

// PipeSegment 管段台账。
type PipeSegment struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	Code          string     `gorm:"size:64;uniqueIndex;not null" json:"code"`
	Name          string     `gorm:"size:128;not null" json:"name"`
	District      string     `gorm:"size:64;index;not null" json:"district"`
	RoadName      string     `gorm:"size:128" json:"roadName"`
	PipeType      string     `gorm:"size:16;index;not null" json:"pipeType"`
	Material      string     `gorm:"size:32" json:"material"`
	DiameterMm    int        `gorm:"not null" json:"diameterMm"`
	LengthM       float64    `gorm:"not null" json:"lengthM"`
	DepthM        float64    `json:"depthM"`
	StartManhole  string     `gorm:"size:64" json:"startManhole"`
	EndManhole    string     `gorm:"size:64" json:"endManhole"`
	BuildYear     int        `json:"buildYear"`
	OwnerUnit     string     `gorm:"size:128" json:"ownerUnit"`
	Status        string     `gorm:"size:16;index;not null;default:normal" json:"status"`
	LastCleanedAt *date.Date `gorm:"type:date" json:"lastCleanedAt"`
	CleanedTimes  int        `gorm:"not null;default:0" json:"cleanedTimes"`
	Remark        string     `gorm:"type:text" json:"remark"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
}

// TableName 指定表名。
func (PipeSegment) TableName() string {
	return "pipe_segments"
}

// CleaningLedger 管段清淤台账流水。只追加、不改写：合格时写入 +1，回退时写入 -1。
//
// 管段表上的累计次数和最近清淤时间由未被冲销的 +1 流水重算；-1 流水记录原作业月份，
// 冲销发生时间保存在 reversed_at，历史月份已对外给出的统计不做物理改写。
type CleaningLedger struct {
	ID                   uint       `gorm:"primaryKey" json:"id"`
	SegmentID            uint       `gorm:"index;not null" json:"segmentId"`
	TaskID               uint       `gorm:"index;not null" json:"taskId"`
	SourceAcceptanceID   *uint      `gorm:"index" json:"sourceAcceptanceId"`
	CleanedAt            date.Date  `gorm:"type:date;not null" json:"cleanedAt"`
	AcceptedAt           date.Date  `gorm:"type:date;not null" json:"acceptedAt"`
	EventType           string     `gorm:"size:16;index;not null" json:"eventType"`
	Delta               int        `gorm:"not null" json:"delta"`
	ReversedAcceptanceID *uint     `gorm:"uniqueIndex" json:"-"`
	ReversalReason      string     `gorm:"size:32" json:"reversalReason"`
	ReversedAt          *time.Time `gorm:"index" json:"reversedAt"`
	CreatedAt            time.Time  `json:"createdAt"`
	UpdatedAt            time.Time  `json:"updatedAt"`
}

// TableName 指定表名。
func (CleaningLedger) TableName() string {
	return "pipe_segment_cleaning_ledger"
}

// Brief 管段精简信息，供其他模块拼接展示。
type Brief struct {
	ID       uint   `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	District string `json:"district"`
	RoadName string `json:"roadName"`
}
