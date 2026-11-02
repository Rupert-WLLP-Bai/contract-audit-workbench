package operationauthority

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"contractaudit/internal/accounts"
	"contractaudit/internal/coreauthority"
	"contractaudit/internal/model"
)

// Projection is accepted only over the configured authority channel. It is not
// a public User input and its scope must equal the operation being authorized.
type Projection struct {
	Scope           model.DelegatedScope `json:"scope"`
	User            accounts.User        `json:"user"`
	PrincipalID     string               `json:"principal_id,omitempty"`
	MembershipID    string               `json:"membership_id,omitempty"`
	MembershipBound bool                 `json:"membership_bound"`
}

type OperationClient struct {
	endpoint, credential string
	http                 *http.Client
}

func NewOperationClient(origin, caller, credential, caFile, appEnv string, insecure bool) (*OperationClient, error) {
	if (caller != "worker" && caller != "document" && caller != "model") || len(credential) < 32 || strings.TrimSpace(credential) != credential {
		return nil, errors.New("operation authority requires an exact service identity and private credential")
	}
	endpoint, client, err := coreauthority.NewTransport(origin, "/internal/v1/"+caller+"/operation-authority", caFile, appEnv, insecure, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &OperationClient{endpoint, credential, client}, nil
}

func (c *OperationClient) ResolveOperation(ctx context.Context, scope model.DelegatedScope) (accounts.User, error) {
	body, err := json.Marshal(scope)
	if err != nil {
		return accounts.User{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return accounts.User{}, errors.New("operation authority request unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+c.credential)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return accounts.User{}, errors.New("operation authority connection unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return accounts.User{}, errors.New("operation authority refused")
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return accounts.User{}, errors.New("operation authority response unavailable")
	}
	var p Projection
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF || !reflect.DeepEqual(p.Scope, scope) {
		return accounts.User{}, errors.New("operation authority scope mismatch")
	}
	u := p.User
	u.PrincipalID = p.PrincipalID
	u.MembershipID = p.MembershipID
	if !u.Enabled || u.ID == "" || u.Role == "" || u.TenantID != scope.TenantID || u.OrgID == "" || u.ID != scope.ActorID {
		return accounts.User{}, errors.New("operation authority identity mismatch")
	}
	if p.MembershipBound {
		if u.PrincipalID == "" || u.MembershipID == "" || u.Issuer == "" || u.Subject == "" {
			return accounts.User{}, errors.New("operation authority membership incomplete")
		}
		restored := accounts.MembershipUser(u.ID, u.DisplayName, u.Role, u.TenantID, u.OrgID, u.Issuer, u.Subject, u.Grants)
		restored.PrincipalID = u.PrincipalID
		restored.MembershipID = u.MembershipID
		u = restored
	} else if u.PrincipalID != "" || u.MembershipID != "" || u.Issuer != "" || u.Subject != "" {
		return accounts.User{}, errors.New("unbound operation authority contains enterprise identity")
	}
	return u, nil
}

func (c *OperationClient) IssueOperationTicket(ctx context.Context, digest string, scope model.DelegatedScope) (string, error) {
	body, err := json.Marshal(struct {
		Digest string               `json:"session_digest"`
		Scope  model.DelegatedScope `json:"scope"`
	}{digest, scope})
	if err != nil {
		return "", err
	}
	endpoint := strings.TrimSuffix(c.endpoint, "/operation-authority") + "/operation-ticket"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.credential)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return "", errors.New("operation ticket connection unavailable")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", errors.New("operation ticket refused")
	}
	var out struct {
		Ticket string `json:"ticket"`
	}
	d := json.NewDecoder(io.LimitReader(res.Body, 16384))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF {
		return "", errors.New("operation ticket invalid")
	}
	return out.Ticket, nil
}

func (c *OperationClient) CloseIdleConnections() {
	if c != nil && c.http != nil {
		c.http.CloseIdleConnections()
	}
}
