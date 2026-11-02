package store

import (
	"context"
	"errors"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/rulepack"
	"github.com/jackc/pgx/v5"
)

// RulePackSuiteResult is a synthetic run pinned to persisted versions. No case
// content or lifecycle state is modified by a run.
type RulePackSuiteResult struct {
	Revision        int64                       `json:"revision"`
	ContentHash     string                      `json:"content_hash"`
	Suite           rulepack.FixtureRun         `json:"suite"`
	Comparison      *rulepack.FixtureComparison `json:"comparison,omitempty"`
	CompareRevision int64                       `json:"compare_revision,omitempty"`
}

func (s *Store) TestRulePackSuite(user accounts.User, input RulePackTestInput) (RulePackSuiteResult, error) {
	admin, err := trustedAdmin(user)
	if err != nil {
		return RulePackSuiteResult{}, err
	}
	if !input.RunFixtureSuite || input.ExpectedRevision < 1 || input.ExpectedContentHash == "" ||
		input.BusinessLine != "" || input.Scenario != "" || input.ContractType != "" || input.At != "" ||
		len(input.Sources) != 0 || len(input.Facts) != 0 {
		return RulePackSuiteResult{}, fault.Invalid("样本套件请求必须指定当前修订和内容摘要，不能混合单样本字段")
	}
	if input.CompareVersion == "" {
		if input.CompareExpectedRevision != 0 || input.CompareExpectedHash != "" {
			return RulePackSuiteResult{}, fault.Invalid("对比修订和摘要必须同时指定版本")
		}
	} else if !validIdentifier(input.CompareVersion) || input.CompareExpectedRevision < 1 || input.CompareExpectedHash == "" {
		return RulePackSuiteResult{}, fault.Invalid("对比版本必须固定版本、修订和内容摘要")
	}
	var result RulePackSuiteResult
	err = s.updateAdminAs(admin, func(tx pgx.Tx) error {
		ctx := commandContext(context.Background(), user)
		tenant := accounts.Tenant(admin)
		if err := lockGovernancePack(ctx, tx, tenant, input.PackID); err != nil {
			return err
		}
		row, err := loadGovernanceRow(ctx, tx, tenant, input.PackID, input.Version)
		if errors.Is(err, errGovernanceVersionMissing) {
			return fault.NotFound("没有找到对应规则包版本")
		}
		if err != nil {
			return err
		}
		pack, err := fixtureGovernancePack(row, input.ExpectedRevision, input.ExpectedContentHash)
		if err != nil {
			return err
		}
		run, err := rulepack.RunFixtureSuite(ctx, pack)
		if err != nil {
			return fault.Invalid("无法运行样本套件：" + err.Error())
		}
		result = RulePackSuiteResult{Revision: row.Version.Revision, ContentHash: row.Version.ContentHash, Suite: run}
		if input.CompareVersion != "" {
			other, err := loadGovernanceRow(ctx, tx, tenant, input.PackID, input.CompareVersion)
			if errors.Is(err, errGovernanceVersionMissing) {
				return fault.NotFound("没有找到对比规则包版本")
			}
			if err != nil {
				return err
			}
			baseline, err := fixtureGovernancePack(other, input.CompareExpectedRevision, input.CompareExpectedHash)
			if err != nil {
				return err
			}
			comparison, err := rulepack.CompareFixtureSuite(ctx, pack, baseline, run)
			if err != nil {
				return fault.Invalid("无法对比样本套件：" + err.Error())
			}
			result.Comparison, result.CompareRevision = &comparison, other.Version.Revision
		}
		pointerRevision, err := currentGovernancePointerRevision(ctx, tx, tenant, input.PackID)
		if err != nil {
			return err
		}
		return insertGovernanceHistory(ctx, tx, RulePackHistoryEntry{
			TenantID: tenant, PackID: input.PackID, Version: input.Version, Action: GovernanceActionTest,
			ActorID: admin.ID, ContentHash: row.Version.ContentHash, PointerRevision: pointerRevision, CreatedAt: nowUTC(),
		})
	})
	return result, err
}

func fixtureGovernancePack(row governanceRow, revision int64, hash string) (*rulepack.Pack, error) {
	if row.Version.Revision != revision || row.Version.ContentHash != hash {
		return nil, fault.Conflict("规则包版本或内容已变化，请刷新后重试")
	}
	pack, err := rulepack.Parse(row.Version.Manifest)
	if err != nil || pack.Hash() != row.Version.ContentHash || sha256Bytes(row.Version.Manifest) != row.Version.SnapshotDigest {
		return nil, fault.Conflict("规则包快照或内容摘要不一致")
	}
	manifest := pack.Manifest()
	if manifest.ID != row.Version.ID || manifest.Version != row.Version.Version || manifest.TenantID != row.Version.TenantID || string(manifest.Lifecycle.State) != row.Version.Status {
		return nil, fault.Conflict("规则包快照身份或状态不一致")
	}
	return pack, nil
}
