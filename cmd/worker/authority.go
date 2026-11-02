package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"contractaudit/internal/coreauthority"
	"contractaudit/internal/operationauthority"
	"contractaudit/internal/store"
)

const workerClaimAuthorityPath = "/internal/v1/worker/claim-authority"

type workerClaimAuthority interface {
	Resolve(context.Context, store.JobClaim) error
}

type coreWorkerClaimAuthority struct {
	endpoint string
	http     *http.Client
	token    string
}

type workerClaimAuthorityWire = operationauthority.ClaimRequest
type workerClaimAuthorityResponse = operationauthority.ClaimProjection

func workerClaimAuthorityFromEnv() (workerClaimAuthority, error) {
	origin := strings.TrimSpace(os.Getenv("CORE_AUTHORITY_URL"))
	caFile := strings.TrimSpace(os.Getenv("CORE_AUTHORITY_CA_FILE"))
	appEnvRaw := strings.TrimSpace(os.Getenv("APP_ENV"))
	appEnv := strings.ToLower(appEnvRaw)
	if appEnv == "" {
		appEnv = "development"
	}
	allowInsecure := false
	if raw := strings.TrimSpace(os.Getenv("CORE_AUTHORITY_ALLOW_INSECURE_DEV")); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, errors.New("CORE_AUTHORITY_ALLOW_INSECURE_DEV must be true or false")
		}
		allowInsecure = value
		if value && appEnvRaw == "" {
			return nil, errors.New("CORE_AUTHORITY_ALLOW_INSECURE_DEV requires explicit APP_ENV=development or test")
		}
	}
	if origin == "" {
		return nil, errors.New("CORE_AUTHORITY_URL is required for worker claim identity checks")
	}
	token := strings.TrimSpace(os.Getenv("WORKER_CORE_TOKEN"))
	if len(token) < 32 {
		return nil, errors.New("WORKER_CORE_TOKEN must contain at least 32 bytes")
	}
	endpoint, client, err := coreauthority.NewTransport(origin, workerClaimAuthorityPath, caFile, appEnv, allowInsecure, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &coreWorkerClaimAuthority{endpoint: endpoint, http: client, token: token}, nil
}

func (resolver *coreWorkerClaimAuthority) Resolve(ctx context.Context, claim store.JobClaim) error {
	if resolver == nil || ctx == nil || claim.JobID == "" || claim.WorkerID == "" || claim.Generation <= 0 ||
		claim.TenantID == "" || claim.OrgID == "" || claim.CaseID == "" || claim.RunID == "" || claim.AttemptID == "" || claim.ActorID == "" {
		return errors.New("worker claim identity is incomplete")
	}
	body, err := json.Marshal(workerClaimAuthorityWire{TenantID: claim.TenantID, OrgID: claim.OrgID, JobID: claim.JobID,
		WorkerID: claim.WorkerID, Generation: claim.Generation, CaseID: claim.CaseID, RunID: claim.RunID, AttemptID: claim.AttemptID})
	if err != nil {
		return errors.New("worker claim identity request cannot be encoded")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, resolver.endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("worker claim identity request is unavailable")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+resolver.token)
	response, err := resolver.http.Do(request)
	if err != nil {
		return fmt.Errorf("Core worker claim identity request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("Core rejected the current worker claim identity")
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, (32<<10)+1))
	if err != nil || len(responseBody) > 32<<10 {
		return errors.New("Core worker claim identity response is unavailable")
	}
	var authority workerClaimAuthorityResponse
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&authority); err != nil {
		return errors.New("Core worker claim identity response is invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("Core worker claim identity response has trailing data")
	}
	if authority.JobID != claim.JobID || authority.CaseID != claim.CaseID || authority.RunID != claim.RunID || authority.AttemptID != claim.AttemptID ||
		authority.Audience != "worker_claim" || !authority.Enabled || authority.ActorID != claim.ActorID ||
		authority.TenantID != claim.TenantID || authority.OrgID != claim.OrgID || authority.Role == "" {
		return errors.New("Core worker claim identity response does not match the active claim")
	}
	if authority.MembershipBound {
		if authority.UserOrgID == "" {
			return errors.New("Core worker membership organization unavailable")
		}
		if authority.Issuer == "" || authority.Subject == "" || authority.PrincipalID == "" || authority.MembershipID == "" {
			return errors.New("Core worker claim identity response has incomplete membership binding")
		}
	} else if authority.Issuer != "" || authority.Subject != "" || authority.PrincipalID != "" || authority.MembershipID != "" {
		return errors.New("unbound Core worker claim response contains enterprise identity fields")
	}
	return nil
}
