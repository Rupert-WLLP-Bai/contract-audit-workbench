package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/fault"
	"contractaudit/internal/model"
	"github.com/jackc/pgx/v5"
)

var moneyMinorRE = regexp.MustCompile(`^(0|[1-9][0-9]{0,29})$`)
var signedMoneyMinorRE = regexp.MustCompile(`^(0|-?[1-9][0-9]{0,29})$`)

type TenantMoneyWindow struct {
	PeriodKey        string  `json:"period_key"`
	LimitMinor       *string `json:"limit_minor"`
	ReservedMinor    string  `json:"reserved_minor"`
	SettledMinor     string  `json:"settled_minor"`
	UnknownHoldMinor string  `json:"unknown_hold_minor"`
	ExposureMinor    string  `json:"exposure_minor"`
}

type TenantMoneyPolicy struct {
	SchemaVersion string            `json:"schema_version"`
	TenantID      string            `json:"tenant_id"`
	Revision      string            `json:"revision"`
	Configured    bool              `json:"configured"`
	Currency      string            `json:"currency"`
	MinorScale    int               `json:"minor_unit_scale"`
	Timezone      string            `json:"timezone"`
	PriceState    string            `json:"price_state"`
	DayLimit      *string           `json:"day_limit_minor"`
	MonthLimit    *string           `json:"month_limit_minor"`
	Day           TenantMoneyWindow `json:"day"`
	Month         TenantMoneyWindow `json:"month"`
}

type TrustedModelPriceInput struct {
	ProviderConfigID string `json:"provider_config_id"`
	ModelRef         string `json:"model_ref"`
	InputRate        string `json:"input_micro_usd_per_million"`
	OutputRate       string `json:"output_micro_usd_per_million"`
}

type PlatformMoneyPolicyInput struct {
	ExpectedRevision string                   `json:"expected_revision"`
	GlobalDayLimit   *string                  `json:"global_day_limit_minor"`
	GlobalMonthLimit *string                  `json:"global_month_limit_minor"`
	Prices           []TrustedModelPriceInput `json:"prices"`
}

type PlatformMoneyPolicyResult struct {
	SchemaVersion string `json:"schema_version"`
	Revision      string `json:"revision"`
	PricesAdded   int    `json:"prices_added"`
}

type PlatformMoneyPolicyView struct {
	SchemaVersion    string  `json:"schema_version"`
	Revision         string  `json:"revision"`
	GlobalDayLimit   *string `json:"global_day_limit_minor"`
	GlobalMonthLimit *string `json:"global_month_limit_minor"`
	PriceSnapshots   string  `json:"price_snapshot_count"`
}

type TenantMoneyAdjustmentInput struct {
	LeaseID        string `json:"lease_id"`
	Kind           string `json:"kind"`
	AmountMinor    string `json:"amount_minor"`
	IdempotencyKey string `json:"idempotency_key"`
	Reason         string `json:"reason"`
}

type TenantMoneyAdjustmentResult struct {
	EventID     string `json:"event_id"`
	LeaseID     string `json:"lease_id"`
	Kind        string `json:"kind"`
	AmountMinor string `json:"amount_minor"`
}

// ReadTenantMoneyPolicy returns the current tenant's exact minor-unit budget
// projection. Missing trusted prices are exposed as unconfigured; they are
// never represented as zero cost.
func (s *Store) ReadTenantMoneyPolicy(ctx context.Context, user accounts.User) (TenantMoneyPolicy, error) {
	current, err := trustedUser(user)
	if err != nil {
		return TenantMoneyPolicy{}, err
	}
	if current.Role != "admin" || current.TenantID == "" {
		return TenantMoneyPolicy{}, fault.Forbidden("只有当前租户管理员可以读取金额预算")
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return TenantMoneyPolicy{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return TenantMoneyPolicy{}, err
	}
	var observed time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&observed); err != nil {
		return TenantMoneyPolicy{}, err
	}
	day, month := observed.UTC().Format("2006-01-02"), observed.UTC().Format("2006-01-02")[:7]+"-01"
	out := TenantMoneyPolicy{SchemaVersion: "tenant-cost-policy/v1", TenantID: current.TenantID, Revision: "0", Currency: "USD", MinorScale: 6, Timezone: "UTC", PriceState: "unconfigured",
		Day:   TenantMoneyWindow{PeriodKey: day, ReservedMinor: "0", SettledMinor: "0", UnknownHoldMinor: "0", ExposureMinor: "0"},
		Month: TenantMoneyWindow{PeriodKey: month[:7], ReservedMinor: "0", SettledMinor: "0", UnknownHoldMinor: "0", ExposureMinor: "0"}}
	var revision int64
	var dayLimit, monthLimit *string
	err = tx.QueryRow(ctx, `SELECT revision,day_limit_minor::text,month_limit_minor::text FROM model_money_limits WHERE scope='tenant' AND tenant_id=$1`, current.TenantID).Scan(&revision, &dayLimit, &monthLimit)
	if err == nil {
		out.Revision = strconv.FormatInt(revision, 10)
		out.DayLimit = dayLimit
		out.MonthLimit = monthLimit
		out.Configured = dayLimit != nil || monthLimit != nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return TenantMoneyPolicy{}, err
	}
	// A known state requires prices for both models of the currently active,
	// server-selected endpoint. A price for an unrelated provider is not enough.
	var endpoint LLMEndpointSettings
	var modelIDs []byte
	err = tx.QueryRow(ctx, `SELECT protocol,base_url,models,review_model,ocr_model FROM llm_endpoint_settings WHERE id='active'`).Scan(&endpoint.Protocol, &endpoint.BaseURL, &modelIDs, &endpoint.ReviewModel, &endpoint.OCRModel)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return TenantMoneyPolicy{}, err
	}
	if err == nil {
		if err := json.Unmarshal(modelIDs, &endpoint.Models); err != nil {
			return TenantMoneyPolicy{}, err
		}
		if endpoint.Active() {
			var known bool
			provider := model.ConfigurationIDFor(endpoint.BaseURL, endpoint.ReviewModel, endpoint.OCRModel)
			if err := tx.QueryRow(ctx, `SELECT model_money_price_known($1,$2,$3)`, provider, endpoint.ReviewModel, endpoint.OCRModel).Scan(&known); err != nil {
				return TenantMoneyPolicy{}, err
			}
			if known {
				out.PriceState = "known"
			}
		}
	}
	var dayReserved, daySettled, dayUnknown, dayExposure string
	if err := tx.QueryRow(ctx, `SELECT
		COALESCE((SELECT sum(money_reserved_minor) FROM model_call_leases WHERE tenant_id=$1 AND money_period_day=$2::date),0)::text,
		COALESCE((SELECT sum(amount_minor) FROM model_money_ledger WHERE tenant_id=$1 AND period_day=$2::date AND event_kind IN ('estimated_cost','observed_cost','adjustment','reconciliation')),0)::text,
		COALESCE((SELECT sum(u.money_reserved_minor) FROM model_usage u WHERE u.tenant_id=$1 AND u.money_period_day=$2::date AND u.money_cost_state='unknown_hold' AND NOT EXISTS(SELECT 1 FROM model_money_ledger l WHERE l.tenant_id=u.tenant_id AND l.lease_id=u.lease_id AND l.event_kind IN ('observed_cost','reconciliation'))),0)::text,
		COALESCE((SELECT sum(amount_minor) FROM model_money_ledger WHERE tenant_id=$1 AND period_day=$2::date),0)::text`, current.TenantID, day).Scan(&dayReserved, &daySettled, &dayUnknown, &dayExposure); err != nil {
		return TenantMoneyPolicy{}, err
	}
	var monthReserved, monthSettled, monthUnknown, monthExposure string
	if err := tx.QueryRow(ctx, `SELECT
		COALESCE((SELECT sum(money_reserved_minor) FROM model_call_leases WHERE tenant_id=$1 AND money_period_month=$2::date),0)::text,
		COALESCE((SELECT sum(amount_minor) FROM model_money_ledger WHERE tenant_id=$1 AND period_month=$2::date AND event_kind IN ('estimated_cost','observed_cost','adjustment','reconciliation')),0)::text,
		COALESCE((SELECT sum(u.money_reserved_minor) FROM model_usage u WHERE u.tenant_id=$1 AND u.money_period_month=$2::date AND u.money_cost_state='unknown_hold' AND NOT EXISTS(SELECT 1 FROM model_money_ledger l WHERE l.tenant_id=u.tenant_id AND l.lease_id=u.lease_id AND l.event_kind IN ('observed_cost','reconciliation'))),0)::text,
		COALESCE((SELECT sum(amount_minor) FROM model_money_ledger WHERE tenant_id=$1 AND period_month=$2::date),0)::text`, current.TenantID, month).Scan(&monthReserved, &monthSettled, &monthUnknown, &monthExposure); err != nil {
		return TenantMoneyPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantMoneyPolicy{}, err
	}
	out.Day.LimitMinor = dayLimit
	out.Month.LimitMinor = monthLimit
	out.Day.ReservedMinor, out.Day.SettledMinor, out.Day.UnknownHoldMinor, out.Day.ExposureMinor = dayReserved, daySettled, dayUnknown, dayExposure
	out.Month.ReservedMinor, out.Month.SettledMinor, out.Month.UnknownHoldMinor, out.Month.ExposureMinor = monthReserved, monthSettled, monthUnknown, monthExposure
	return out, nil
}

// SetTenantMoneyLimits is revision guarded and only writes the actor's own
// tenant row. Caps are canonical USD micro-unit strings; nil removes a cap.
func (s *Store) SetTenantMoneyLimits(ctx context.Context, user accounts.User, expected string, dayLimit, monthLimit *string) (TenantMoneyPolicy, error) {
	current, err := trustedUser(user)
	if err != nil {
		return TenantMoneyPolicy{}, err
	}
	if current.Role != "admin" || current.TenantID == "" {
		return TenantMoneyPolicy{}, fault.Forbidden("只有当前租户管理员可以修改自身金额预算")
	}
	if s.ReadOnly() {
		return TenantMoneyPolicy{}, fault.New(503, "read_only", "维护模式不允许修改金额预算")
	}
	expectedRevision, err := strconv.ParseInt(expected, 10, 64)
	if err != nil || expectedRevision < 0 {
		return TenantMoneyPolicy{}, fault.Invalid("金额预算 revision 无效")
	}
	for _, v := range []*string{dayLimit, monthLimit} {
		if v != nil && !moneyMinorRE.MatchString(*v) {
			return TenantMoneyPolicy{}, fault.Invalid("金额上限必须是非负 canonical minor-unit 整数字符串")
		}
	}
	ctx = commandContext(ctx, current)
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TenantMoneyPolicy{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return TenantMoneyPolicy{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema()||':model-money:global',0)), pg_advisory_xact_lock(hashtextextended(current_schema()||':model-money:tenant:'||$1,0))`, current.TenantID); err != nil {
		return TenantMoneyPolicy{}, err
	}
	var existing int64
	err = tx.QueryRow(ctx, `SELECT revision FROM model_money_limits WHERE scope='tenant' AND tenant_id=$1 FOR UPDATE`, current.TenantID).Scan(&existing)
	if errors.Is(err, pgx.ErrNoRows) {
		existing = 0
	} else if err != nil {
		return TenantMoneyPolicy{}, err
	}
	if existing != expectedRevision {
		return TenantMoneyPolicy{}, fault.New(409, "money_policy_revision_conflict", "金额预算已更新，请刷新后重试")
	}
	var revision int64
	err = tx.QueryRow(ctx, `INSERT INTO model_money_limits(scope,tenant_id,day_limit_minor,month_limit_minor,revision,timezone,updated_by)
		VALUES('tenant',$1,$2::numeric,$3::numeric,1,'UTC',$4)
		ON CONFLICT(scope,tenant_id) DO UPDATE SET day_limit_minor=EXCLUDED.day_limit_minor,month_limit_minor=EXCLUDED.month_limit_minor,
		revision=model_money_limits.revision+1,updated_by=EXCLUDED.updated_by,updated_at=clock_timestamp()
		WHERE model_money_limits.revision=$5 RETURNING revision`, current.TenantID, dayLimit, monthLimit, current.ID, expectedRevision).Scan(&revision)
	if err != nil {
		return TenantMoneyPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantMoneyPolicy{}, err
	}
	return s.ReadTenantMoneyPolicy(ctx, current)
}

// PublishPlatformMoneyPolicy only accepts actors and actual PostgreSQL login
// principals listed in server configuration. The price rows are immutable;
// revisions publish new snapshots rather than rewriting call prices.
func (s *Store) PublishPlatformMoneyPolicy(ctx context.Context, user accounts.User, input PlatformMoneyPolicyInput) (PlatformMoneyPolicyResult, error) {
	current, err := trustedUser(user)
	if err != nil {
		return PlatformMoneyPolicyResult{}, err
	}
	if current.Role != "admin" || !configuredAllowlistContains("CAW_PLATFORM_OPERATOR_ACTORS", current.ID) {
		return PlatformMoneyPolicyResult{}, fault.Forbidden("当前身份未获平台金额策略授权")
	}
	if s.ReadOnly() {
		return PlatformMoneyPolicyResult{}, fault.New(503, "read_only", "维护模式不允许修改平台金额策略")
	}
	expected, err := strconv.ParseInt(input.ExpectedRevision, 10, 64)
	if err != nil || expected < 0 {
		return PlatformMoneyPolicyResult{}, fault.Invalid("平台金额策略 revision 无效")
	}
	for _, v := range []*string{input.GlobalDayLimit, input.GlobalMonthLimit} {
		if v != nil && !moneyMinorRE.MatchString(*v) {
			return PlatformMoneyPolicyResult{}, fault.Invalid("全局金额上限必须是非负 canonical minor-unit 整数字符串")
		}
	}
	for _, p := range input.Prices {
		if strings.TrimSpace(p.ProviderConfigID) == "" || strings.TrimSpace(p.ModelRef) == "" || !moneyMinorRE.MatchString(p.InputRate) || !moneyMinorRE.MatchString(p.OutputRate) {
			return PlatformMoneyPolicyResult{}, fault.Invalid("可信模型价格字段无效")
		}
	}
	ctx = commandContext(ctx, current)
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return PlatformMoneyPolicyResult{}, err
	}
	defer tx.Rollback(ctx)
	if current.TenantID != "" {
		if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
			return PlatformMoneyPolicyResult{}, err
		}
	} else if _, err := tx.Exec(ctx, `SELECT set_config('app.actor_id',$1,true)`, current.ID); err != nil {
		return PlatformMoneyPolicyResult{}, err
	}
	var sessionUser, dbUser string
	if err := tx.QueryRow(ctx, `SELECT session_user,current_user`).Scan(&sessionUser, &dbUser); err != nil {
		return PlatformMoneyPolicyResult{}, err
	}
	if sessionUser == "" || dbUser == "" || sessionUser != dbUser || !configuredAllowlistContains("CAW_PLATFORM_OPERATOR_DATABASE_PRINCIPALS", sessionUser) {
		return PlatformMoneyPolicyResult{}, fault.Forbidden("当前 PostgreSQL 登录主体未获平台金额策略授权")
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.platform_operator_actor',$1,true)`, current.ID); err != nil {
		return PlatformMoneyPolicyResult{}, err
	}
	for _, price := range input.Prices {
		id, err := newID("mps_")
		if err != nil {
			return PlatformMoneyPolicyResult{}, err
		}
		payload, _ := json.Marshal(price)
		digest := sha256.Sum256(payload)
		if _, err := tx.Exec(ctx, `SELECT model_money_publish_price($1,$2,$3,$4::numeric,$5::numeric,$6,$7)`, id, price.ProviderConfigID, price.ModelRef, price.InputRate, price.OutputRate, hex.EncodeToString(digest[:]), current.ID); err != nil {
			return PlatformMoneyPolicyResult{}, err
		}
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT model_money_publish_global($1,$2::numeric,$3::numeric,$4)`, expected, input.GlobalDayLimit, input.GlobalMonthLimit, current.ID).Scan(&revision); err != nil {
		var state interface{ SQLState() string }
		if errors.As(err, &state) && state.SQLState() == "P7M04" {
			return PlatformMoneyPolicyResult{}, fault.New(409, "money_policy_revision_conflict", "平台金额策略已更新，请刷新后重试")
		}
		return PlatformMoneyPolicyResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PlatformMoneyPolicyResult{}, err
	}
	return PlatformMoneyPolicyResult{SchemaVersion: "platform-money-policy/v1", Revision: strconv.FormatInt(revision, 10), PricesAdded: len(input.Prices)}, nil
}

func (s *Store) ReadPlatformMoneyPolicy(ctx context.Context, user accounts.User) (PlatformMoneyPolicyView, error) {
	current, err := trustedUser(user)
	if err != nil {
		return PlatformMoneyPolicyView{}, err
	}
	if current.Role != "admin" || !configuredAllowlistContains("CAW_PLATFORM_OPERATOR_ACTORS", current.ID) {
		return PlatformMoneyPolicyView{}, fault.Forbidden("当前身份未获平台金额策略授权")
	}
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return PlatformMoneyPolicyView{}, err
	}
	defer tx.Rollback(ctx)
	if current.TenantID != "" {
		if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
			return PlatformMoneyPolicyView{}, err
		}
	} else if _, err := tx.Exec(ctx, `SELECT set_config('app.actor_id',$1,true)`, current.ID); err != nil {
		return PlatformMoneyPolicyView{}, err
	}
	var sessionUser, dbUser string
	if err := tx.QueryRow(ctx, `SELECT session_user,current_user`).Scan(&sessionUser, &dbUser); err != nil {
		return PlatformMoneyPolicyView{}, err
	}
	if sessionUser == "" || dbUser == "" || sessionUser != dbUser || !configuredAllowlistContains("CAW_PLATFORM_OPERATOR_DATABASE_PRINCIPALS", sessionUser) {
		return PlatformMoneyPolicyView{}, fault.Forbidden("当前 PostgreSQL 登录主体未获平台金额策略授权")
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('app.platform_operator_actor',$1,true)`, current.ID); err != nil {
		return PlatformMoneyPolicyView{}, err
	}
	out := PlatformMoneyPolicyView{SchemaVersion: "platform-money-policy/v1", Revision: "0", PriceSnapshots: "0"}
	if err := tx.QueryRow(ctx, `SELECT revision::text,day_limit_minor,month_limit_minor,price_count::text FROM model_money_global_control()`).Scan(&out.Revision, &out.GlobalDayLimit, &out.GlobalMonthLimit, &out.PriceSnapshots); err != nil {
		return PlatformMoneyPolicyView{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PlatformMoneyPolicyView{}, err
	}
	return out, nil
}

func (s *Store) AppendTenantMoneyAdjustment(ctx context.Context, user accounts.User, input TenantMoneyAdjustmentInput) (TenantMoneyAdjustmentResult, error) {
	current, err := trustedUser(user)
	if err != nil {
		return TenantMoneyAdjustmentResult{}, err
	}
	if current.Role != "admin" || current.TenantID == "" {
		return TenantMoneyAdjustmentResult{}, fault.Forbidden("只有当前租户管理员可以登记自身费用更正")
	}
	if s.ReadOnly() {
		return TenantMoneyAdjustmentResult{}, fault.New(503, "read_only", "维护模式不允许登记金额更正")
	}
	if strings.TrimSpace(input.LeaseID) == "" || input.Kind != "adjustment" && input.Kind != "reconciliation" || !signedMoneyMinorRE.MatchString(input.AmountMinor) || strings.TrimSpace(input.IdempotencyKey) == "" || len(input.IdempotencyKey) > 160 || len(strings.TrimSpace(input.Reason)) < 8 || len(input.Reason) > 1000 {
		return TenantMoneyAdjustmentResult{}, fault.Invalid("费用更正字段无效")
	}
	ctx = commandContext(ctx, current)
	tx, err := s.beginContextTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TenantMoneyAdjustmentResult{}, err
	}
	defer tx.Rollback(ctx)
	if err := setLocalScope(ctx, tx, userScope(current)); err != nil {
		return TenantMoneyAdjustmentResult{}, err
	}
	payload, _ := json.Marshal(struct{ Tenant, Lease, Kind, Amount, Key, Reason string }{current.TenantID, input.LeaseID, input.Kind, input.AmountMinor, input.IdempotencyKey, strings.TrimSpace(input.Reason)})
	digest := sha256.Sum256(payload)
	var eventID string
	if err := tx.QueryRow(ctx, `SELECT model_money_adjust_tenant($1,$2,$3,$4::numeric,$5,$6,$7,$8)`, current.TenantID, input.LeaseID, input.Kind, input.AmountMinor, input.IdempotencyKey, hex.EncodeToString(digest[:]), strings.TrimSpace(input.Reason), current.ID).Scan(&eventID); err != nil {
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			switch state.SQLState() {
			case "P7M06":
				return TenantMoneyAdjustmentResult{}, fault.New(409, "money_adjustment_conflict", "幂等键已用于不同费用更正")
			case "P7M05":
				return TenantMoneyAdjustmentResult{}, fault.New(404, "model_call_not_found", "原始模型调用没有可更正的可信价格账本")
			}
		}
		return TenantMoneyAdjustmentResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return TenantMoneyAdjustmentResult{}, err
	}
	return TenantMoneyAdjustmentResult{EventID: eventID, LeaseID: input.LeaseID, Kind: input.Kind, AmountMinor: input.AmountMinor}, nil
}

func configuredAllowlistContains(envName, value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	for _, entry := range strings.Split(os.Getenv(envName), ",") {
		if strings.TrimSpace(entry) == value {
			return true
		}
	}
	return false
}
