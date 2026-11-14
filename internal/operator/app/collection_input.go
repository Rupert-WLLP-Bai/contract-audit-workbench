// File metadata (manifest mirror):
// Purpose: Owns the original collection key and business-time preparation, dedicated-line scenario rule and confirmed binding constraints without moving authority or clocks.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-14
// Updated: 2026-11-14
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package app

import (
	"strings"
	"time"

	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

func PrepareCollectionInput(key string, businessAt time.Time) (string, time.Time, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", time.Time{}, fault.Invalid("缺少幂等键")
	}
	if businessAt.IsZero() {
		return "", time.Time{}, fault.Invalid("业务时间不合法")
	}
	return key, businessAt.UTC().Truncate(time.Microsecond), nil
}
func ValidateCollectionScenario(scenario string) error {
	if scenario != "telecom_dedicated_line" {
		return fault.Conflict("当前案件场景不支持专线跨系统核验")
	}
	return nil
}
func ValidateCollectionBinding(binding model.OperatorObjectBinding, businessAt time.Time, connectorID, externalObjectID string) error {
	if binding.Status != "confirmed" {
		return fault.Conflict("对象映射尚未确认，不能采集外部证据")
	}
	if businessAt.Before(binding.EffectiveFrom) || !businessAt.Before(binding.EffectiveUntil) {
		return fault.Conflict("业务时间不在已确认对象映射的有效期间内")
	}
	if connectorID != binding.ExternalObject.ConnectorID || externalObjectID != binding.ExternalObject.ExternalObjectID {
		return fault.Conflict("采集请求与已确认的对象映射不一致")
	}
	return nil
}
