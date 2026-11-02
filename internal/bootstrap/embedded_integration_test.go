// File metadata (manifest mirror):
// Purpose: 实现 `TestEmbeddedBootstrapIndependentModelAuthorityAndLedgerPG` 等单进程服务装配与集成入口，连接已配置的服务组件和权限边界。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-02
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package bootstrap

import (
	"context"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/config"
	"contractaudit/internal/httpapi"
	"contractaudit/internal/model"
	"contractaudit/internal/modelgateway"
	"contractaudit/internal/operationauthority"
	"contractaudit/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEmbeddedBootstrapIndependentModelAuthorityAndLedgerPG(t *testing.T) {
	ctx := context.Background()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Fatal("isolated TEST_DATABASE_URL required")
	}
	t.Setenv("APP_ENV", "test")
	t.Setenv("LLM_BASE_URL", "")
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("MODEL_GATEWAY_URL", "")
	t.Setenv("WORKER_MODEL_TOKEN", "")
	t.Setenv("DOCUMENT_MODEL_TOKEN", "")
	t.Setenv("CORE_MODEL_AUTHORITY_TOKEN", "")
	core, err := store.OpenTest(t.TempDir(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	t.Setenv("DATABASE_SCHEMA", core.SchemaName())
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	var suffix string
	if err = admin.QueryRow(ctx, `SELECT substr(md5(random()::text),1,12)`).Scan(&suffix); err != nil {
		t.Fatal(err)
	}
	var roleNames []string
	for _, service := range []string{"core", "document", "execution", "model", "tool"} {
		name := "embedded_" + service + "_" + suffix
		roleNames = append(roleNames, name)
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		parsed.User = url.UserPassword(name, strings.Repeat("x", 40))
		t.Setenv(strings.ToUpper(service)+"_DATABASE_URL", parsed.String())
	}
	t.Cleanup(func() {
		defer admin.Close()
		for _, name := range roleNames {
			if _, err := admin.Exec(context.Background(), "DROP ROLE IF EXISTS "+pgx.Identifier{name}.Sanitize()); err != nil {
				t.Errorf("drop isolated test role: %v", err)
			}
		}
	})
	if err = store.MigrateServiceRoles(ctx); err != nil {
		t.Fatalf("provision isolated roles: %v", err)
	}
	selection, err := config.Select(config.Environ{AppEnv: config.ModeTest, TestDatabaseURL: os.Getenv("MODEL_DATABASE_URL")})
	if err != nil {
		t.Fatal(err)
	}
	modelStore, err := store.OpenApplicationSelection(t.TempDir(), selection)
	if err != nil {
		t.Fatal(err)
	}
	defer modelStore.Close()
	if err = modelStore.UseServiceOwner("model"); err != nil {
		t.Fatal(err)
	}
	if err = modelStore.ValidateServiceDatabaseRole(ctx, "model"); err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("a", 40)
	worker := strings.Repeat("w", 40)
	document := strings.Repeat("d", 40)
	handler, err := httpapi.DevelopmentHandler(core, httpapi.Options{ModelAuthorityCredential: secret}, "test", worker, document)
	if err != nil {
		t.Fatal(err)
	}
	authorityServer := httptest.NewServer(handler)
	defer authorityServer.Close()
	authority, err := operationauthority.NewOperationClient(authorityServer.URL, "model", secret, "", "test", true)
	if err != nil {
		t.Fatal(err)
	}
	modelStore.UseOperationAuthority(authority)
	provider := "http://provider.invalid/v1"
	cfg := modelgateway.Config{Mode: "fixture", ProviderURL: provider, ChatModel: "fixture-chat", OCRModel: "fixture-ocr", WorkerCredential: worker, DocumentCredential: document, FixtureOCRContent: "采购文件要求质保36个月。", FixtureChatContent: `{"claim":"期限差异","relation":"conflict","disposition":"risk","quotes":[]}`, MaxConcurrency: 4, QueueCapacity: 8, RequestTimeout: 3 * time.Second, CircuitFailureThreshold: 5, CircuitCooldown: time.Minute, Budget: store.ModelBudgetLimits{GlobalMaxInFlight: 8, TenantMaxInFlight: 8, GlobalCallsPerMinute: 100, TenantCallsPerMinute: 100, GlobalTokensPerMinute: 4000000, TenantTokensPerMinute: 4000000, MaxInputTokenReservation: 1 << 20, MaxOutputTokenReservation: 8192, LeaseDuration: time.Minute}}
	gateway, err := modelgateway.New(modelStore, cfg)
	if err != nil {
		t.Fatal(err)
	}
	gatewayServer := httptest.NewServer(gateway.Handler())
	defer gatewayServer.Close()
	t.Setenv("LLM_BASE_URL", provider)
	t.Setenv("LLM_MODEL", cfg.ChatModel)
	t.Setenv("OCR_MODEL", cfg.OCRModel)
	t.Setenv("MODEL_GATEWAY_URL", gatewayServer.URL+"/v1")
	t.Setenv("WORKER_MODEL_TOKEN", worker)
	t.Setenv("DOCUMENT_MODEL_TOKEN", document)
	t.Setenv("CORE_MODEL_AUTHORITY_TOKEN", secret)
	profile, ok := accounts.ByID("demo-admin")
	if !ok {
		t.Fatal("profile missing")
	}
	token, err := core.EstablishSession(profile, "client")
	if err != nil {
		t.Fatal(err)
	}
	user, _, err := core.AuthenticateSession(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	ctx = store.WithAdmissionSessionToken(ctx, token)
	caseID, err := core.CreateCase(user, "independent model gateway", "procurement_ict")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = core.AddDocument(ctx, user, caseID, "procurement", "scan.png", "image/png", model.SampleWarrantyPNG); err != nil {
		t.Fatal(err)
	}
	if _, err = core.AddDocument(ctx, user, caseID, "draft_contract", "draft.txt", "text/plain", []byte("拟签合同约定质保24个月。")); err != nil {
		t.Fatal(err)
	}
	modelConfig, err := serviceModelConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = wireRuntimeStore(ctx, core, runtimeSettings{modelConfig: modelConfig}, ""); err != nil {
		t.Fatal(err)
	}
	runID, err := core.StartReview(ctx, user, caseID)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(25 * time.Second)
	for {
		ws, err := core.Workspace(user, caseID, runID)
		if err != nil {
			t.Fatal(err)
		}
		if ws.Execution != nil && ws.Execution.Attempt != nil && ws.Execution.Attempt.State != "accepted" && ws.Execution.Attempt.State != "running" {
			if ws.Execution.Attempt.State == "failed" {
				t.Fatalf("review failed: %+v", ws.Execution.Attempt)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("review did not finish")
		}
		time.Sleep(15 * time.Millisecond)
	}
	rows, err := admin.Query(ctx, fmt.Sprintf(`SELECT caller_identity,model_ref,call_status FROM %s.model_usage WHERE case_id=$1`, pgx.Identifier{core.SchemaName()}.Sanitize()), caseID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var caller, modelRef, status string
		if err = rows.Scan(&caller, &modelRef, &status); err != nil {
			t.Fatal(err)
		}
		if status == "completed" {
			counts[caller+":"+modelRef]++
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if counts["document:fixture-ocr"] != 1 || counts["worker:fixture-chat"] != 1 {
		t.Fatalf("expected settled OCR and review: %+v", counts)
	}
}
