import { useQuery } from '@tanstack/react-query'
import React, { useEffect } from 'react'
import { Row, Col, Statistic, Space, Tooltip, Spin } from 'antd'
import { useLingui } from '@lingui/react/macro'
import { FontAwesomeIcon } from '@fortawesome/react-fontawesome'
import {
  faPaperPlane,
  faCircleCheck,
  faEye,
  faCircleXmark,
  faFaceFrown
} from '@fortawesome/free-regular-svg-icons'
import { faArrowPointer, faTriangleExclamation, faBan } from '@fortawesome/free-solid-svg-icons'
import { getBroadcastStats } from '../../services/api/messages_history'
import { broadcastApi } from '../../services/api/broadcast'
import { useNavigate } from '@tanstack/react-router'
import type { Workspace } from '../../services/api/types'

export interface ProgressStats {
  remaining: number
  processed: number
  enqueuedCount: number
  sentCount: number
  failedCount: number
  retryingCount: number
  latestError?: string
}

interface BroadcastStatsProps {
  workspaceId: string
  broadcastId: string
  workspace?: Workspace
  enqueuedCount?: number
  broadcastStatus?: string
  onStatsUpdate?: (stats: ProgressStats) => void
}

export function BroadcastStats({
  workspaceId,
  broadcastId,
  workspace,
  enqueuedCount,
  broadcastStatus,
  onStatsUpdate
}: BroadcastStatsProps) {
  const { t } = useLingui()
  const navigate = useNavigate()

  const { data, isLoading } = useQuery({
    queryKey: ['broadcast-stats', workspaceId, broadcastId],
    queryFn: async () => {
      return getBroadcastStats(workspaceId, broadcastId)
    },
    // Stop polling for terminal states where stats won't change.
    refetchInterval:
      broadcastStatus === 'cancelled' || broadcastStatus === 'failed' ? false : 5000
  })

  const { data: deliveryData } = useQuery({
    queryKey: ['broadcast-delivery-status', workspaceId, broadcastId],
    queryFn: () =>
      broadcastApi.getDeliveryStatus({ workspace_id: workspaceId, id: broadcastId }),
    enabled: broadcastStatus !== 'draft' && broadcastStatus !== 'scheduled',
    refetchInterval:
      broadcastStatus === 'cancelled' || broadcastStatus === 'failed' ? false : 5000
  })

  const stats = data?.stats || {
    total_sent: 0,
    total_delivered: 0,
    total_opened: 0,
    total_clicked: 0,
    total_failed: 0,
    total_bounced: 0,
    total_complained: 0,
    total_unsubscribed: 0
  }

  const delivery = deliveryData?.delivery
  // A failed attempt has a sent_at row too, so total_failed is a subset of
  // total_sent. Only exhausted failures are terminal; automatic retries still
  // count as remaining delivery work.
  const successful = Math.max(0, stats.total_sent - stats.total_failed)
  const exhausted = delivery?.exhausted || 0
  const processed = successful + exhausted
  const remaining = enqueuedCount ? Math.max(0, enqueuedCount - processed) : 0

  // Notify parent of stats changes using a ref to avoid re-renders
  const onStatsUpdateRef = React.useRef(onStatsUpdate)
  useEffect(() => {
    onStatsUpdateRef.current = onStatsUpdate
  })

  useEffect(() => {
    if (enqueuedCount !== undefined) {
      onStatsUpdateRef.current?.({
        remaining,
        processed,
        enqueuedCount,
        sentCount: successful,
        failedCount: exhausted,
        retryingCount: delivery?.retrying || 0,
        latestError: delivery?.latest_error
      })
    }
  }, [
    remaining,
    processed,
    enqueuedCount,
    successful,
    exhausted,
    delivery?.retrying,
    delivery?.latest_error
  ])

  // Check if marketing provider is SMTP
  const isSmtpProvider = (() => {
    if (!workspace?.settings?.marketing_email_provider_id) return false
    const marketingProviderId = workspace.settings.marketing_email_provider_id
    const integration = workspace.integrations?.find((i) => i.id === marketingProviderId)
    return integration?.email_provider?.kind === 'smtp'
  })()

  const getRate = (numerator: number, denominator: number) => {
    if (denominator === 0) return '-'
    const percentage = (numerator / denominator) * 100
    if (percentage === 0 || percentage >= 10) {
      return `${Math.round(percentage)}%`
    }
    return `${percentage.toFixed(1)}%`
  }

  // Function to navigate to logs page with specific filter
  const navigateToLogs = (filterType: string) => {
    const searchParams = new URLSearchParams()
    searchParams.set('broadcast_id', broadcastId)

    if (filterType !== 'sent') {
      searchParams.set(filterType, 'true')
    }

    const url = `/console/workspace/${workspaceId}/logs?${searchParams.toString()}`
    navigate({ to: url as string & {} })
  }

  // Formatter function for statistics that handles loading state
  const formatStat = (value: number | string) => {
    if (isLoading) {
      return <Spin size="small" />
    }
    return value
  }

  return (
    <Row gutter={[16, 16]} wrap className="flex-nowrap overflow-x-auto">
      <Col span={3}>
        <Tooltip title={t`${successful} total emails sent - Click to view details`}>
          <div
            className="cursor-pointer hover:bg-gray-50 p-2 rounded transition-colors"
            onClick={() => navigateToLogs('sent')}
          >
            <Statistic
              title={
                <Space className="font-medium">
                  <FontAwesomeIcon
                    icon={faPaperPlane}
                    style={{ opacity: 0.7 }}
                    className="text-blue-500"
                  />{' '}
                  {t`Sent`}
                </Space>
              }
              value={successful}
              styles={{ content: { fontSize: '16px' } }}
              formatter={formatStat}
            />
          </div>
        </Tooltip>
      </Col>
      <Col span={3}>
        <Tooltip
          title={
            isSmtpProvider
              ? t`SMTP provider doesn't support delivery webhooks, so delivery status cannot be tracked`
              : t`${stats.total_delivered} emails successfully delivered - Click to view details`
          }
        >
          <div
            className={`p-2 rounded transition-colors ${
              isSmtpProvider
                ? 'cursor-not-allowed opacity-50'
                : 'cursor-pointer hover:bg-gray-50'
            }`}
            onClick={isSmtpProvider ? undefined : () => navigateToLogs('is_delivered')}
          >
            <Statistic
              title={
                <Space className="font-medium">
                  <FontAwesomeIcon
                    icon={faCircleCheck}
                    style={{ opacity: 0.7 }}
                    className="text-green-500"
                  />{' '}
                  {t`Delivered`}
                </Space>
              }
              value={isSmtpProvider ? '-' : getRate(stats.total_delivered, successful)}
              styles={{ content: { fontSize: '16px' } }}
              formatter={formatStat}
            />
          </div>
        </Tooltip>
      </Col>
      <Col span={3}>
        <Tooltip title={t`${stats.total_opened} total opens - Click to view details`}>
          <div
            className="cursor-pointer hover:bg-gray-50 p-2 rounded transition-colors"
            onClick={() => navigateToLogs('is_opened')}
          >
            <Statistic
              title={
                <Space className="font-medium">
                  <FontAwesomeIcon
                    icon={faEye}
                    style={{ opacity: 0.7 }}
                    className="text-purple-500"
                  />{' '}
                  {t`Opens`}
                </Space>
              }
              value={getRate(stats.total_opened, successful)}
              styles={{ content: { fontSize: '16px' } }}
              formatter={formatStat}
            />
          </div>
        </Tooltip>
      </Col>
      <Col span={3}>
        <Tooltip title={t`${stats.total_clicked} total clicks - Click to view details`}>
          <div
            className="cursor-pointer hover:bg-gray-50 p-2 rounded transition-colors"
            onClick={() => navigateToLogs('is_clicked')}
          >
            <Statistic
              title={
                <Space className="font-medium">
                  <FontAwesomeIcon
                    icon={faArrowPointer}
                    style={{ opacity: 0.7 }}
                    className="text-cyan-500 mr-1"
                  />{' '}
                  {t`Clicks`}
                </Space>
              }
              value={getRate(stats.total_clicked, successful)}
              styles={{ content: { fontSize: '16px' } }}
              formatter={formatStat}
            />
          </div>
        </Tooltip>
      </Col>
      <Col span={3}>
        <Tooltip title={t`${stats.total_failed} emails failed to send - Click to view details`}>
          <div
            className="cursor-pointer hover:bg-gray-50 p-2 rounded transition-colors"
            onClick={() => navigateToLogs('is_failed')}
          >
            <Statistic
              title={
                <Space className="font-medium">
                  <FontAwesomeIcon
                    icon={faCircleXmark}
                    style={{ opacity: 0.7 }}
                    className="text-orange-500"
                  />{' '}
                  {t`Failed`}
                </Space>
              }
              value={getRate(stats.total_failed, enqueuedCount || stats.total_sent)}
              styles={{ content: { fontSize: '16px' } }}
              formatter={formatStat}
            />
          </div>
        </Tooltip>
      </Col>
      <Col span={3}>
        <Tooltip
          title={
            isSmtpProvider
              ? t`SMTP provider doesn't support bounce webhooks, so bounce status cannot be tracked`
              : t`${stats.total_bounced} emails bounced back - Click to view details`
          }
        >
          <div
            className={`p-2 rounded transition-colors ${
              isSmtpProvider
                ? 'cursor-not-allowed opacity-50'
                : 'cursor-pointer hover:bg-gray-50'
            }`}
            onClick={isSmtpProvider ? undefined : () => navigateToLogs('is_bounced')}
          >
            <Statistic
              title={
                <Space className="font-medium">
                  <FontAwesomeIcon
                    icon={faTriangleExclamation}
                    style={{ opacity: 0.7 }}
                    className="text-orange-500"
                  />{' '}
                  {t`Bounced`}
                </Space>
              }
              value={isSmtpProvider ? '-' : getRate(stats.total_bounced, successful)}
              styles={{ content: { fontSize: '16px' } }}
              formatter={formatStat}
            />
          </div>
        </Tooltip>
      </Col>
      <Col span={3}>
        <Tooltip
          title={
            isSmtpProvider
              ? t`SMTP provider doesn't support complaint webhooks, so spam complaints cannot be tracked`
              : t`${stats.total_complained} total complaints - Click to view details`
          }
        >
          <div
            className={`p-2 rounded transition-colors ${
              isSmtpProvider
                ? 'cursor-not-allowed opacity-50'
                : 'cursor-pointer hover:bg-gray-50'
            }`}
            onClick={isSmtpProvider ? undefined : () => navigateToLogs('is_complained')}
          >
            <Statistic
              title={
                <Space className="font-medium">
                  <FontAwesomeIcon
                    icon={faFaceFrown}
                    style={{ opacity: 0.7 }}
                    className="text-orange-500"
                  />{' '}
                  {t`Complaints`}
                </Space>
              }
              value={isSmtpProvider ? '-' : getRate(stats.total_complained, successful)}
              styles={{ content: { fontSize: '16px' } }}
              formatter={formatStat}
            />
          </div>
        </Tooltip>
      </Col>
      <Col span={3}>
        <Tooltip title={t`${stats.total_unsubscribed} total unsubscribes - Click to view details`}>
          <div
            className="cursor-pointer hover:bg-gray-50 p-2 rounded transition-colors"
            onClick={() => navigateToLogs('is_unsubscribed')}
          >
            <Statistic
              title={
                <Space className="font-medium">
                  <FontAwesomeIcon
                    icon={faBan}
                    style={{ opacity: 0.7 }}
                    className="text-orange-500"
                  />{' '}
                  {t`Unsub.`}
                </Space>
              }
              value={getRate(stats.total_unsubscribed, successful)}
              styles={{ content: { fontSize: '16px' } }}
              formatter={formatStat}
            />
          </div>
        </Tooltip>
      </Col>
    </Row>
  )
}
