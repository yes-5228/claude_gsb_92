// 验收详情：验收结论明细 + 清淤成果汇总 + 整改登记。
import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { acceptanceApi } from '../../api/acceptances';
import { toErrorMessage } from '../../api/client';
import { ConfirmDialog } from '../../components/ConfirmDialog';
import { InfoList } from '../../components/InfoList';
import { Modal } from '../../components/Modal';
import { PageHeader } from '../../components/PageHeader';
import { SectionCard } from '../../components/SectionCard';
import { StatCard } from '../../components/StatCard';
import { StatusTag } from '../../components/StatusTag';
import { StateBlock } from '../../components/StateBlock';
import { useToast } from '../../components/Toast';
import { useAsync } from '../../hooks/useAsync';
import { formatDate, formatDateTime, formatLength, formatNumber, formatVolume, today } from '../../utils/format';

export function AcceptanceDetailPage() {
  const params = useParams();
  const navigate = useNavigate();
  const toast = useToast();
  const id = Number(params.id ?? '0');

  const detail = useAsync(
    () => (id > 0 ? acceptanceApi.detail(id) : Promise.reject(new Error('验收记录编号无效'))),
    [id]
  );

  const [rectifyOpen, setRectifyOpen] = useState(false);
  const [rectifiedAt, setRectifiedAt] = useState(today());
  const [rectification, setRectification] = useState('');
  const [remark, setRemark] = useState('');
  const [busy, setBusy] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const acceptance = detail.data?.acceptance;
  const task = detail.data?.task;
  const totals = detail.data?.recordTotals;
  const needRectify = acceptance?.result === 'rework' && !acceptance.rectifiedAt;

  const submitRectify = async () => {
    setBusy(true);
    try {
      await acceptanceApi.rectify(id, { rectifiedAt, rectification: rectification.trim(), remark: remark.trim() });
      toast.success('整改完成情况已登记，请重新提交完工报验');
      setRectifyOpen(false);
      setRectification('');
      setRemark('');
      detail.reload();
    } catch (cause: unknown) {
      toast.error(toErrorMessage(cause));
    } finally {
      setBusy(false);
    }
  };

  const submitDelete = async () => {
    setBusy(true);
    try {
      await acceptanceApi.remove(id);
      toast.success('验收记录已删除，台账已按当前有效合格任务重算');
      navigate('/acceptances');
    } catch (cause: unknown) {
      toast.error(toErrorMessage(cause));
      setDeleteOpen(false);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="page">
      <PageHeader
        title={acceptance ? `${acceptance.code} 验收记录` : '验收记录详情'}
        description="验收合格会同步任务状态并重算管段清淤台账；需整改则任务回到「清淤中」，登记整改并重新报验后复验。"
        extra={acceptance ? <StatusTag list="acceptanceResults" value={acceptance.result} /> : null}
        actions={
          <>
            <button type="button" className="btn btn-ghost" onClick={() => navigate('/acceptances')}>
              返回列表
            </button>
            {task ? (
              <button type="button" className="btn btn-ghost" onClick={() => navigate(`/tasks/${task.id}`)}>
                查看任务
              </button>
            ) : null}
            {needRectify ? (
              <button type="button" className="btn btn-primary" onClick={() => setRectifyOpen(true)}>
                登记整改完成
              </button>
            ) : null}
            <button type="button" className="btn btn-danger" onClick={() => setDeleteOpen(true)}>
              删除
            </button>
          </>
        }
      />

      <StateBlock
        loading={detail.loading}
        error={detail.error}
        onRetry={detail.reload}
        empty={!acceptance}
        emptyText="验收记录不存在"
      >
        {acceptance ? (
          <>
            {needRectify ? (
              <div className="alert alert-warn">
                <p>
                  该验收结论为「需整改」，整改期限 {formatDate(acceptance.rectifyDeadline)}
                  ，登记整改完成并重新完工报验后才能复验。
                </p>
              </div>
            ) : null}
            {acceptance.result === 'rework' && acceptance.rectifiedAt ? (
              <div className="alert alert-success">
                <p>整改已于 {formatDate(acceptance.rectifiedAt)} 完成，请重新提交完工报验后复验。</p>
              </div>
            ) : null}

            <SectionCard title="验收结论" subtitle={`登记于 ${formatDateTime(acceptance.createdAt)}`}>
              <InfoList
                items={[
                  { label: '验收编号', value: acceptance.code },
                  { label: '验收日期', value: formatDate(acceptance.acceptedAt) },
                  { label: '验收结论', value: <StatusTag list="acceptanceResults" value={acceptance.result} /> },
                  { label: '验收评分', value: `${acceptance.score} 分` },
                  { label: '残留淤积厚度', value: `${formatNumber(acceptance.residualSludgeMm, 1)} mm` },
                  { label: '关联清淤记录', value: acceptance.cleaningRecordId ? `#${acceptance.cleaningRecordId}` : '未指定' },
                  { label: '验收人', value: acceptance.inspectorName },
                  { label: '验收单位', value: acceptance.inspectorOrg || '—' },
                  { label: '整改期限', value: formatDate(acceptance.rectifyDeadline) },
                  { label: '整改完成日期', value: formatDate(acceptance.rectifiedAt) },
                  { label: '存在问题', value: acceptance.issues || '—', span: 3 },
                  { label: '整改要求', value: acceptance.rectification || '—', span: 3 },
                  { label: '备注', value: acceptance.remark || '—', span: 3 }
                ]}
              />
            </SectionCard>

            <SectionCard
              title="关联任务与清淤成果"
              subtitle="验收对象的任务信息与该任务下的清淤汇总"
              extra={
                task ? (
                  <Link className="link" to={`/tasks/${task.id}`}>
                    查看任务详情
                  </Link>
                ) : null
              }
            >
              {task ? (
                <InfoList
                  items={[
                    { label: '任务编号', value: task.code },
                    { label: '任务标题', value: task.title },
                    { label: '任务状态', value: <StatusTag list="taskStatuses" value={task.status} /> },
                    { label: '优先级', value: <StatusTag list="taskPriorities" value={task.priority} /> },
                    { label: '实施班组', value: task.teamName || '—' },
                    { label: '关联管段', value: `${task.segmentCode} · ${task.segmentName}` },
                    { label: '所属片区', value: task.segmentDistrict || '—' }
                  ]}
                />
              ) : (
                <p className="form-note">关联任务已不存在。</p>
              )}
              <div style={{ height: 16 }} />
              <div className="stat-grid">
                <StatCard label="清淤记录条数" value={formatNumber(totals?.recordCount ?? 0, 0)} tone="primary" />
                <StatCard label="累计清淤量" value={formatVolume(totals?.sludgeVolumeM3 ?? 0)} />
                <StatCard label="累计清淤长度" value={formatLength(totals?.cleanedLengthM ?? 0)} />
                <StatCard label="最近清淤日期" value={formatDate(totals?.latestCleanedAt)} />
              </div>
            </SectionCard>
          </>
        ) : null}
      </StateBlock>

      <ConfirmDialog
        open={deleteOpen}
        title="删除验收记录"
        danger
        busy={busy}
        confirmText="确认删除"
        message={
          acceptance?.result === 'pass' ? (
            <p>删除合格验收后，任务将退回待验收，管段清淤次数与最近清淤时间会同步回退；原始清淤记录和历史月份清淤量不会删除或改写。</p>
          ) : (
            <p>删除需整改验收后，任务将退回待验收，可重新登记验收结论。</p>
          )
        }
        onConfirm={() => void submitDelete()}
        onCancel={() => setDeleteOpen(false)}
      />

      <Modal
        open={rectifyOpen}
        title="登记整改完成"
        onClose={() => setRectifyOpen(false)}
        footer={
          <>
            <button type="button" className="btn btn-ghost" onClick={() => setRectifyOpen(false)}>
              放弃
            </button>
            <button type="button" className="btn btn-primary" disabled={busy} onClick={() => void submitRectify()}>
              {busy ? '提交中…' : '提交整改结果'}
            </button>
          </>
        }
      >
        <div className="form-field">
          <span className="form-label">
            整改完成日期
            <em className="form-required">*</em>
          </span>
          <input
            className="input"
            type="date"
            value={rectifiedAt}
            onChange={(event) => setRectifiedAt(event.target.value)}
          />
        </div>
        <div style={{ height: 12 }} />
        <div className="form-field">
          <span className="form-label">整改情况说明</span>
          <textarea
            className="textarea"
            value={rectification}
            placeholder="例如：已完成二次清淤并复测残留淤积厚度 12mm"
            onChange={(event) => setRectification(event.target.value)}
          />
        </div>
        <div style={{ height: 12 }} />
        <div className="form-field">
          <span className="form-label">备注</span>
          <textarea className="textarea" value={remark} onChange={(event) => setRemark(event.target.value)} />
        </div>
      </Modal>
    </div>
  );
}
