package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"contractaudit/internal/fault"
	"contractaudit/internal/operator/model"
)

// ServiceObjectRequestSHA256 preserves the historical command digest envelope.
func ServiceObjectRequestSHA256(id CreateIdentity, operation string, payload any) (string, error) {
	raw, err := json.Marshal(struct {
		TenantID  string `json:"tenant_id"`
		OrgID     string `json:"org_id"`
		CaseID    string `json:"case_id"`
		ActorID   string `json:"actor_id"`
		Operation string `json:"operation"`
		Payload   any    `json:"payload"`
	}{id.TenantID, id.OrgID, id.CaseID, id.ActorID, operation, payload})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func decodeServiceObjectReceipt(receipt Receipt, hash string) (model.ServiceObjectMutationResult, error) {
	var result model.ServiceObjectMutationResult
	if receipt.RequestSHA != hash {
		return result, fault.Conflict("同一幂等键不能用于不同服务对象请求")
	}
	if receipt.Status < 200 || receipt.Status >= 300 || len(receipt.Body) == 0 {
		return result, fault.New(500, "integrity_error", "服务对象幂等回执无效")
	}
	if err := json.Unmarshal(receipt.Body, &result); err != nil {
		return model.ServiceObjectMutationResult{}, fault.New(500, "integrity_error", "服务对象幂等回执无法读取")
	}
	return result, nil
}
