package cleaningtask_test

import (
	"context"
	"testing"

	"github.com/drainage/desilting/internal/httpx"
	"github.com/drainage/desilting/internal/modules/cleaningtask"
	"github.com/drainage/desilting/internal/modules/dashboard"
	"github.com/drainage/desilting/internal/shared/date"
	"github.com/drainage/desilting/internal/testsupport"
)

func TestRecordEntryMovesTaskToInProgress(t *testing.T) {
	fixture := testsupport.NewFixture(t)
	task := fixture.CreateTask(t, fixture.Segment.ID, "首次录入记录的任务")

	if task.Status != cleaningtask.StatusPending {
		t.Fatalf("新建任务应为待开工，实际 %s", task.Status)
	}

	fixture.CreateRecord(t, task.ID, 10)

	if got := fixture.Reload(t, task.ID).Status; got != cleaningtask.StatusInProgress {
		t.Fatalf("录入清淤记录后应自动进入清淤中，实际 %s", got)
	}
}

func TestCompleteRequiresCleaningRecord(t *testing.T) {
	fixture := testsupport.NewFixture(t)
	task := fixture.CreateTask(t, fixture.Segment.ID, "未录入记录的任务")
	testsupport.RequireNoError(t, ignoreTask(fixture.Tasks.Start(context.Background(), task.ID)))

	_, err := fixture.Tasks.Complete(context.Background(), task.ID)
	appErr := testsupport.RequireAppError(t, err, httpx.CodeInvalidState)
	if appErr.Message == "" {
		t.Fatal("错误提示不应为空")
	}
}

func TestCompleteRejectsPendingTask(t *testing.T) {
	fixture := testsupport.NewFixture(t)
	task := fixture.CreateTask(t, fixture.Segment.ID, "未开工的任务")

	_, err := fixture.Tasks.Complete(context.Background(), task.ID)
	testsupport.RequireAppError(t, err, httpx.CodeInvalidState)
}

func TestCancelRequiresReason(t *testing.T) {
	fixture := testsupport.NewFixture(t)
	task := fixture.CreateTask(t, fixture.Segment.ID, "待取消的任务")

	_, err := fixture.Tasks.Cancel(context.Background(), task.ID, "   ")
	testsupport.RequireAppError(t, err, httpx.CodeValidation)

	cancelled, err := fixture.Tasks.Cancel(context.Background(), task.ID, "汛期调度调整")
	testsupport.RequireNoError(t, err)
	if cancelled.Status != cleaningtask.StatusCancelled {
		t.Fatalf("期望任务状态为已取消，实际 %s", cancelled.Status)
	}
}

func TestCancelledTaskCannotBeStarted(t *testing.T) {
	fixture := testsupport.NewFixture(t)
	task := fixture.CreateTask(t, fixture.Segment.ID, "已取消的任务")
	testsupport.RequireNoError(t, ignoreTask(fixture.Tasks.Cancel(context.Background(), task.ID, "计划调整")))

	_, err := fixture.Tasks.Start(context.Background(), task.ID)
	testsupport.RequireAppError(t, err, httpx.CodeInvalidState)
}

func TestCancelAcceptedTaskRollsBackSegmentLedger(t *testing.T) {
	fixture := testsupport.NewFixture(t)
	first := fixture.TaskReadyForAcceptance(t, fixture.Segment.ID, "已验收任务一")
	_, err := fixture.Acceptances.Create(context.Background(), testsupport.PassRequest(first.ID, 90))
	testsupport.RequireNoError(t, err)

	second := fixture.TaskReadyForAcceptance(t, fixture.Segment.ID, "已验收任务二")
	_, err = fixture.Acceptances.Create(context.Background(), testsupport.PassRequest(second.ID, 90))
	testsupport.RequireNoError(t, err)

	dashboardService := dashboard.NewService(fixture.DB)
	before, err := dashboardService.Overview(context.Background())
	testsupport.RequireNoError(t, err)

	cancelled, err := fixture.Tasks.Cancel(context.Background(), first.ID, "验收结论录入错误，撤销任务")
	testsupport.RequireNoError(t, err)
	if cancelled.Status != cleaningtask.StatusCancelled {
		t.Fatalf("任务应为已取消，实际 %s", cancelled.Status)
	}
	if cancelled.AcceptedAt != nil {
		t.Fatal("取消已验收任务时应清空验收时间")
	}

	segment, err := fixture.Segments.FindByID(context.Background(), fixture.Segment.ID)
	testsupport.RequireNoError(t, err)
	if segment.CleanedTimes != 1 {
		t.Fatalf("取消一批作业后应只保留另一批的次数，实际 %d", segment.CleanedTimes)
	}
	if segment.LastCleanedAt == nil || segment.LastCleanedAt.String() != date.Today().AddDays(-1).String() {
		t.Fatalf("最近清淤时间应回退到剩余合格作业，实际 %+v", segment.LastCleanedAt)
	}

	_, err = fixture.Tasks.Cancel(context.Background(), second.ID, "两批验收结论均需撤销")
	testsupport.RequireNoError(t, err)
	segment, err = fixture.Segments.FindByID(context.Background(), fixture.Segment.ID)
	testsupport.RequireNoError(t, err)
	if segment.CleanedTimes != 0 || segment.LastCleanedAt != nil {
		t.Fatalf("两批作业都取消后台账应完全回退，实际次数=%d 最近清淤=%+v", segment.CleanedTimes, segment.LastCleanedAt)
	}

	after, err := dashboardService.Overview(context.Background())
	testsupport.RequireNoError(t, err)
	if after.UncleanedSegmentCount != before.UncleanedSegmentCount+1 {
		t.Fatalf("回退后台账未清淤数量应增加 1，before=%d after=%d", before.UncleanedSegmentCount, after.UncleanedSegmentCount)
	}
	if after.SludgeThisMonthM3 != before.SludgeThisMonthM3 {
		t.Fatalf("台账回退不应改写按清淤记录计算的历史/当月清淤量，before=%v after=%v", before.SludgeThisMonthM3, after.SludgeThisMonthM3)
	}
}

func TestAcceptedTaskCannotBeEdited(t *testing.T) {
	fixture := testsupport.NewFixture(t)
	task := fixture.TaskReadyForAcceptance(t, fixture.Segment.ID, "已验收的任务")
	_, err := fixture.Acceptances.Create(context.Background(), testsupport.PassRequest(task.ID, 92))
	testsupport.RequireNoError(t, err)

	_, err = fixture.Tasks.Update(context.Background(), task.ID, cleaningtask.SaveRequest{
		Title:         "修改后的标题",
		PipeSegmentID: fixture.Segment.ID,
		PlanStartDate: date.Today().AddDays(-3),
		PlanEndDate:   date.Today().AddDays(1),
	})
	testsupport.RequireAppError(t, err, httpx.CodeInvalidState)
}

func TestPlanEndBeforeStartIsRejected(t *testing.T) {
	fixture := testsupport.NewFixture(t)
	_, err := fixture.Tasks.Create(context.Background(), cleaningtask.SaveRequest{
		Title:         "日期不合理的任务",
		PipeSegmentID: fixture.Segment.ID,
		PlanStartDate: date.Today(),
		PlanEndDate:   date.Today().AddDays(-5),
	})
	testsupport.RequireAppError(t, err, httpx.CodeValidation)
}

func TestTaskRequiresExistingSegment(t *testing.T) {
	fixture := testsupport.NewFixture(t)
	_, err := fixture.Tasks.Create(context.Background(), cleaningtask.SaveRequest{
		Title:         "引用不存在管段的任务",
		PipeSegmentID: 99999,
		PlanStartDate: date.Today(),
		PlanEndDate:   date.Today().AddDays(2),
	})
	testsupport.RequireAppError(t, err, httpx.CodeNotFound)
}

func TestTaskCodeIsUniqueAndSequential(t *testing.T) {
	fixture := testsupport.NewFixture(t)
	first := fixture.CreateTask(t, fixture.Segment.ID, "任务一")
	second := fixture.CreateTask(t, fixture.Segment.ID, "任务二")

	if first.Code == second.Code {
		t.Fatalf("任务编号重复: %s", first.Code)
	}
	if first.Code >= second.Code {
		t.Fatalf("同日任务编号应递增，实际 %s / %s", first.Code, second.Code)
	}
}

func TestDeleteTaskBlockedAfterRecord(t *testing.T) {
	fixture := testsupport.NewFixture(t)
	task := fixture.CreateTask(t, fixture.Segment.ID, "已有记录的任务")
	fixture.CreateRecord(t, task.ID, 8)

	err := fixture.Tasks.Delete(context.Background(), task.ID)
	testsupport.RequireAppError(t, err, httpx.CodeConflict)
}

func TestAllowedActionsFollowStatus(t *testing.T) {
	cases := map[string][]string{
		cleaningtask.StatusPending:   {cleaningtask.ActionStart, cleaningtask.ActionEdit, cleaningtask.ActionCancel},
		cleaningtask.StatusCompleted: {cleaningtask.ActionAccept},
		cleaningtask.StatusAccepted:  {cleaningtask.ActionCancel},
		cleaningtask.StatusCancelled: {},
	}
	for status, want := range cases {
		got := cleaningtask.AllowedActions(status)
		if len(got) != len(want) {
			t.Fatalf("状态 %s 期望可执行操作 %v，实际 %v", status, want, got)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("状态 %s 期望可执行操作 %v，实际 %v", status, want, got)
			}
		}
	}
}

// ignoreTask 丢弃任务返回值，只保留 error，便于在断言里直接使用。
func ignoreTask(_ *cleaningtask.CleaningTask, err error) error {
	return err
}
