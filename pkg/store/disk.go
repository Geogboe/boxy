package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Geogboe/boxy/pkg/lifecycle"
	"github.com/Geogboe/boxy/pkg/model"
)

// DiskStore is a simple JSON-backed Store implementation.
//
// It exists to make the CLI work end-to-end in environments where pulling new
// dependencies (e.g. bbolt) is not possible. It can be replaced with a bbolt
// implementation later without changing the Store interface.
type DiskStore struct {
	mu   sync.Mutex
	path string
	data diskState
}

type diskState struct {
	Pools                  map[model.PoolName]model.Pool                        `json:"pools"`
	PoolGuestCredentials   map[model.PoolName]string                            `json:"pool_guest_credentials"`
	Resources              map[model.ResourceID]model.Resource                  `json:"resources"`
	Sandboxes              map[model.SandboxID]model.Sandbox                    `json:"sandboxes"`
	Executions             map[model.ExecutionID]model.Execution                `json:"executions"`
	AgentTokens            map[model.AgentTokenID]model.AgentRegistrationToken  `json:"agent_tokens"`
	APIKeys                map[model.APIKeyID]model.APIKey                      `json:"api_keys"`
	Sessions               map[model.SessionID]model.Session                    `json:"sessions"`
	LocalAdmin             *model.LocalAdminAccount                             `json:"local_admin,omitempty"`
	RevokedAgentIdentities map[model.AgentIdentityID]model.RevokedAgentIdentity `json:"revoked_agent_identities"`
	AgentIdentities        map[string]model.AgentIdentity                       `json:"agent_identities"`
	Events                 map[string]lifecycle.Record                          `json:"lifecycle_events"`
}

func NewDiskStore(path string) (*DiskStore, error) {
	if path == "" {
		return nil, fmt.Errorf("disk store path is required")
	}
	s := &DiskStore{
		path: path,
		data: diskState{
			Pools:                  make(map[model.PoolName]model.Pool),
			PoolGuestCredentials:   make(map[model.PoolName]string),
			Resources:              make(map[model.ResourceID]model.Resource),
			Sandboxes:              make(map[model.SandboxID]model.Sandbox),
			Executions:             make(map[model.ExecutionID]model.Execution),
			AgentTokens:            make(map[model.AgentTokenID]model.AgentRegistrationToken),
			APIKeys:                make(map[model.APIKeyID]model.APIKey),
			Sessions:               make(map[model.SessionID]model.Session),
			RevokedAgentIdentities: make(map[model.AgentIdentityID]model.RevokedAgentIdentity),
			AgentIdentities:        make(map[string]model.AgentIdentity),
			Events:                 make(map[string]lifecycle.Record),
		},
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *DiskStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read store %q: %w", s.path, err)
	}
	if len(b) == 0 {
		return nil
	}
	var st diskState
	if err := json.Unmarshal(b, &st); err != nil {
		return fmt.Errorf("decode store %q: %w", s.path, err)
	}
	if st.Pools == nil {
		st.Pools = make(map[model.PoolName]model.Pool)
	}
	if st.Resources == nil {
		st.Resources = make(map[model.ResourceID]model.Resource)
	}
	if st.PoolGuestCredentials == nil {
		st.PoolGuestCredentials = make(map[model.PoolName]string)
	}
	if st.Sandboxes == nil {
		st.Sandboxes = make(map[model.SandboxID]model.Sandbox)
	}
	if st.Executions == nil {
		st.Executions = make(map[model.ExecutionID]model.Execution)
	}
	if st.AgentTokens == nil {
		st.AgentTokens = make(map[model.AgentTokenID]model.AgentRegistrationToken)
	}
	if st.APIKeys == nil {
		st.APIKeys = make(map[model.APIKeyID]model.APIKey)
	}
	if st.Sessions == nil {
		st.Sessions = make(map[model.SessionID]model.Session)
	}
	if st.RevokedAgentIdentities == nil {
		st.RevokedAgentIdentities = make(map[model.AgentIdentityID]model.RevokedAgentIdentity)
	}
	if st.AgentIdentities == nil {
		st.AgentIdentities = make(map[string]model.AgentIdentity)
	}
	if st.Events == nil {
		st.Events = make(map[string]lifecycle.Record)
	}
	s.data = st
	return nil
}

func (s *DiskStore) persistLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %q: %w", dir, err)
	}

	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode store: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write store tmp %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename store tmp: %w", err)
	}
	return nil
}

func (s *DiskStore) GetPool(ctx context.Context, name model.PoolName) (model.Pool, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.data.Pools[name]
	if !ok {
		return model.Pool{}, ErrNotFound
	}
	return p, nil
}

func (s *DiskStore) PutPool(ctx context.Context, pool model.Pool) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if pool.Name == "" {
		return fmt.Errorf("pool name is required")
	}
	s.data.Pools[pool.Name] = pool
	return s.persistLocked()
}

func (s *DiskStore) PutPoolGuestCredential(ctx context.Context, poolName model.PoolName, credential string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if poolName == "" {
		return fmt.Errorf("pool name is required")
	}
	if strings.TrimSpace(credential) == "" {
		return fmt.Errorf("pool guest credential is required")
	}
	s.data.PoolGuestCredentials[poolName] = credential
	return s.persistLocked()
}

func (s *DiskStore) GetPoolGuestCredential(ctx context.Context, poolName model.PoolName) (string, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	credential, ok := s.data.PoolGuestCredentials[poolName]
	if !ok {
		return "", ErrNotFound
	}
	return credential, nil
}

// DeletePoolGuestCredential removes a legacy plaintext pool credential. New
// runtime code stores credentials through pkg/secrets; this method exists for
// the explicit migration command only.
func (s *DiskStore) DeletePoolGuestCredential(ctx context.Context, poolName model.PoolName) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.PoolGuestCredentials[poolName]; !ok {
		return ErrNotFound
	}
	delete(s.data.PoolGuestCredentials, poolName)
	return s.persistLocked()
}

func (s *DiskStore) ListPoolGuestCredentials(ctx context.Context) (map[model.PoolName]string, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[model.PoolName]string, len(s.data.PoolGuestCredentials))
	for pool, credential := range s.data.PoolGuestCredentials {
		out[pool] = credential
	}
	return out, nil
}

func (s *DiskStore) GetResource(ctx context.Context, id model.ResourceID) (model.Resource, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.data.Resources[id]
	if !ok {
		return model.Resource{}, ErrNotFound
	}
	return r, nil
}

func (s *DiskStore) PutResource(ctx context.Context, res model.Resource) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if res.ID == "" {
		return fmt.Errorf("resource id is required")
	}
	s.data.Resources[res.ID] = res
	return s.persistLocked()
}

func (s *DiskStore) DeleteResource(_ context.Context, id model.ResourceID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Resources[id]; !ok {
		return ErrNotFound
	}
	delete(s.data.Resources, id)
	return s.persistLocked()
}

func (s *DiskStore) GetSandbox(ctx context.Context, id model.SandboxID) (model.Sandbox, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	sb, ok := s.data.Sandboxes[id]
	if !ok {
		return model.Sandbox{}, ErrNotFound
	}
	return sb, nil
}

func (s *DiskStore) CreateSandbox(ctx context.Context, sb model.Sandbox) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if sb.ID == "" {
		return fmt.Errorf("sandbox id is required")
	}
	if _, exists := s.data.Sandboxes[sb.ID]; exists {
		return fmt.Errorf("sandbox already exists: %s", sb.ID)
	}
	s.data.Sandboxes[sb.ID] = sb
	return s.persistLocked()
}

func (s *DiskStore) PutSandbox(ctx context.Context, sb model.Sandbox) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if sb.ID == "" {
		return fmt.Errorf("sandbox id is required")
	}
	s.data.Sandboxes[sb.ID] = sb
	return s.persistLocked()
}

func (s *DiskStore) DeleteSandbox(_ context.Context, id model.SandboxID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Sandboxes[id]; !ok {
		return ErrNotFound
	}
	delete(s.data.Sandboxes, id)
	return s.persistLocked()
}

func (s *DiskStore) GetExecution(_ context.Context, id model.ExecutionID) (model.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	execution, ok := s.data.Executions[id]
	if !ok {
		return model.Execution{}, ErrNotFound
	}
	return cloneExecution(execution), nil
}

func (s *DiskStore) PutExecution(_ context.Context, execution model.Execution) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if execution.ID == "" {
		return fmt.Errorf("execution id is required")
	}
	if execution.SandboxID == "" || execution.ResourceID == "" {
		return fmt.Errorf("execution sandbox and resource are required")
	}
	s.data.Executions[execution.ID] = cloneExecution(execution)
	return s.persistLocked()
}

func (s *DiskStore) DeleteExecution(_ context.Context, id model.ExecutionID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Executions[id]; !ok {
		return ErrNotFound
	}
	delete(s.data.Executions, id)
	return s.persistLocked()
}

func (s *DiskStore) ListExecutions(_ context.Context) ([]model.Execution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Execution, 0, len(s.data.Executions))
	for _, execution := range s.data.Executions {
		out = append(out, cloneExecution(execution))
	}
	return out, nil
}

func (s *DiskStore) ListPools(_ context.Context) ([]model.Pool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Pool, 0, len(s.data.Pools))
	for _, p := range s.data.Pools {
		out = append(out, p)
	}
	return out, nil
}

func (s *DiskStore) ListResources(_ context.Context) ([]model.Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Resource, 0, len(s.data.Resources))
	for _, r := range s.data.Resources {
		out = append(out, r)
	}
	return out, nil
}

func (s *DiskStore) ListSandboxes(_ context.Context) ([]model.Sandbox, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Sandbox, 0, len(s.data.Sandboxes))
	for _, sb := range s.data.Sandboxes {
		out = append(out, sb)
	}
	return out, nil
}

func (s *DiskStore) GetAgentToken(_ context.Context, id model.AgentTokenID) (model.AgentRegistrationToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tok, ok := s.data.AgentTokens[id]
	if !ok {
		return model.AgentRegistrationToken{}, ErrNotFound
	}
	return tok, nil
}

func (s *DiskStore) PutAgentToken(_ context.Context, tok model.AgentRegistrationToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if tok.ID == "" {
		return fmt.Errorf("agent token id is required")
	}
	s.data.AgentTokens[tok.ID] = tok
	return s.persistLocked()
}

func (s *DiskStore) DeleteAgentToken(_ context.Context, id model.AgentTokenID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.AgentTokens[id]; !ok {
		return ErrNotFound
	}
	delete(s.data.AgentTokens, id)
	return s.persistLocked()
}

func (s *DiskStore) ListAgentTokens(_ context.Context) ([]model.AgentRegistrationToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.AgentRegistrationToken, 0, len(s.data.AgentTokens))
	for _, tok := range s.data.AgentTokens {
		out = append(out, tok)
	}
	return out, nil
}

func (s *DiskStore) GetAPIKey(_ context.Context, id model.APIKeyID) (model.APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.data.APIKeys[id]
	if !ok {
		return model.APIKey{}, ErrNotFound
	}
	return key, nil
}

func (s *DiskStore) PutAPIKey(_ context.Context, key model.APIKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key.ID == "" {
		return fmt.Errorf("api key id is required")
	}
	if key.Hash == "" {
		return fmt.Errorf("api key hash is required")
	}
	if !key.Role.Valid() {
		return fmt.Errorf("api key role %q is invalid", key.Role)
	}
	s.data.APIKeys[key.ID] = key
	return s.persistLocked()
}

func (s *DiskStore) DeleteAPIKey(_ context.Context, id model.APIKeyID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.APIKeys[id]; !ok {
		return ErrNotFound
	}
	delete(s.data.APIKeys, id)
	return s.persistLocked()
}

func (s *DiskStore) ListAPIKeys(_ context.Context) ([]model.APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.APIKey, 0, len(s.data.APIKeys))
	for _, key := range s.data.APIKeys {
		out = append(out, key)
	}
	return out, nil
}

func (s *DiskStore) GetSession(_ context.Context, id model.SessionID) (model.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.data.Sessions[id]
	if !ok {
		return model.Session{}, ErrNotFound
	}
	return session, nil
}

func (s *DiskStore) PutSession(_ context.Context, session model.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if session.ID == "" {
		return fmt.Errorf("session id is required")
	}
	if session.Hash == "" {
		return fmt.Errorf("session hash is required")
	}
	s.data.Sessions[session.ID] = session
	return s.persistLocked()
}

func (s *DiskStore) DeleteSession(_ context.Context, id model.SessionID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Sessions[id]; !ok {
		return ErrNotFound
	}
	delete(s.data.Sessions, id)
	return s.persistLocked()
}

func (s *DiskStore) ListSessions(_ context.Context) ([]model.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Session, 0, len(s.data.Sessions))
	for _, session := range s.data.Sessions {
		out = append(out, session)
	}
	return out, nil
}

func (s *DiskStore) GetLocalAdmin(_ context.Context) (model.LocalAdminAccount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data.LocalAdmin == nil {
		return model.LocalAdminAccount{}, ErrNotFound
	}
	return *s.data.LocalAdmin, nil
}

func (s *DiskStore) PutLocalAdmin(_ context.Context, account model.LocalAdminAccount) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if account.Username == "" {
		return fmt.Errorf("local admin username is required")
	}
	if account.PasswordHash == "" {
		return fmt.Errorf("local admin password hash is required")
	}
	s.data.LocalAdmin = &account
	return s.persistLocked()
}

func (s *DiskStore) PutRevokedAgentIdentity(_ context.Context, rev model.RevokedAgentIdentity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rev.ID == "" {
		return fmt.Errorf("revoked agent identity id is required")
	}
	s.data.RevokedAgentIdentities[rev.ID] = rev
	return s.persistLocked()
}

// IsAgentIdentityRevoked does a linear scan over revoked identities — an
// accepted tradeoff at the expected 10s-100s scale, rather than maintaining
// a secondary index keyed by cert serial.
func (s *DiskStore) IsAgentIdentityRevoked(_ context.Context, certSerial string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rev := range s.data.RevokedAgentIdentities {
		if rev.CertSerial == certSerial {
			return true, nil
		}
	}
	return false, nil
}

func (s *DiskStore) ListRevokedAgentIdentities(_ context.Context) ([]model.RevokedAgentIdentity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.RevokedAgentIdentity, 0, len(s.data.RevokedAgentIdentities))
	for _, rev := range s.data.RevokedAgentIdentities {
		out = append(out, rev)
	}
	return out, nil
}

func (s *DiskStore) PutAgentIdentity(_ context.Context, identity model.AgentIdentity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if identity.AgentID == "" {
		return fmt.Errorf("agent id is required")
	}
	s.data.AgentIdentities[identity.AgentID] = identity
	return s.persistLocked()
}

func (s *DiskStore) GetAgentIdentity(_ context.Context, agentID string) (model.AgentIdentity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	identity, ok := s.data.AgentIdentities[agentID]
	if !ok {
		return model.AgentIdentity{}, ErrNotFound
	}
	return identity, nil
}
