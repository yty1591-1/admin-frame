package memory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
)

// SecretRepositories 汇总密钥子域进程内仓储。
type SecretRepositories struct {
	Secrets domain.SecretRepository
	Grants  domain.SecretGrantRepository
	Access  domain.SecretAccessRepository
}

// NewSecrets 构造空密钥仓储集合。
func NewSecrets() *SecretRepositories {
	store := newSecretStore()
	return &SecretRepositories{
		Secrets: &secretRepository{store: store},
		Grants:  &secretGrantRepository{store: store},
		Access:  &secretAccessRepository{store: store},
	}
}

type secretStore struct {
	mu       sync.RWMutex
	secrets  map[int64]domain.Secret
	nameIdx  map[string]int64
	versions map[int64][]domain.SecretVersion
	reviews  map[int64][]domain.SecretReview
	grants   map[int64]domain.SecretGrant
	accesses []domain.SecretAccess
}

func newSecretStore() *secretStore {
	return &secretStore{
		secrets:  make(map[int64]domain.Secret),
		nameIdx:  make(map[string]int64),
		versions: make(map[int64][]domain.SecretVersion),
		reviews:  make(map[int64][]domain.SecretReview),
		grants:   make(map[int64]domain.SecretGrant),
	}
}

func secretNameKey(env, namespace, name string) string {
	return env + "\x00" + namespace + "\x00" + name
}

type secretRepository struct{ store *secretStore }

func (r *secretRepository) Create(_ context.Context, secret domain.Secret, version domain.SecretVersion) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if _, ok := r.store.secrets[secret.ID]; ok {
		return domain.ErrDuplicateKey
	}
	key := secretNameKey(secret.Env, secret.Namespace, secret.Name)
	if _, ok := r.store.nameIdx[key]; ok {
		return domain.ErrDuplicateKey
	}
	r.store.secrets[secret.ID] = cloneSecret(secret)
	r.store.nameIdx[key] = secret.ID
	r.store.versions[secret.ID] = []domain.SecretVersion{cloneSecretVersion(version)}
	return nil
}

func (r *secretRepository) GetByID(_ context.Context, id int64) (domain.Secret, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	s, ok := r.store.secrets[id]
	if !ok {
		return domain.Secret{}, domain.ErrNotFound
	}
	return withFingerprint(s, r.store.versions[id]), nil
}

func (r *secretRepository) GetByName(_ context.Context, env, namespace, name string) (domain.Secret, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	id, ok := r.store.nameIdx[secretNameKey(env, namespace, name)]
	if !ok {
		return domain.Secret{}, domain.ErrNotFound
	}
	return withFingerprint(r.store.secrets[id], r.store.versions[id]), nil
}

func (r *secretRepository) List(_ context.Context, env, namespace string, status domain.SecretStatus, limit, offset int) ([]domain.Secret, int, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	var all []domain.Secret
	for _, s := range r.store.secrets {
		if s.Env != env || s.Namespace != namespace {
			continue
		}
		if status != "" && s.Status != status {
			continue
		}
		all = append(all, withFingerprint(s, r.store.versions[s.ID]))
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	total := len(all)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if limit <= 0 || end > total {
		end = total
	}
	return all[offset:end], total, nil
}

func (r *secretRepository) UpdateCAS(_ context.Context, secret domain.Secret, expectedVersion int64) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	cur, ok := r.store.secrets[secret.ID]
	if !ok {
		return domain.ErrNotFound
	}
	if cur.Version != expectedVersion {
		return domain.ErrVersionConflict
	}
	r.store.secrets[secret.ID] = cloneSecret(secret)
	return nil
}

func (r *secretRepository) AppendVersion(_ context.Context, version domain.SecretVersion) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	if _, ok := r.store.secrets[version.SecretID]; !ok {
		return domain.ErrNotFound
	}
	for _, existing := range r.store.versions[version.SecretID] {
		if existing.Version == version.Version {
			return domain.ErrDuplicateKey
		}
	}
	r.store.versions[version.SecretID] = append(r.store.versions[version.SecretID], cloneSecretVersion(version))
	return nil
}

func (r *secretRepository) GetVersion(_ context.Context, secretID, version int64) (domain.SecretVersion, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	for _, v := range r.store.versions[secretID] {
		if v.Version == version {
			return cloneSecretVersion(v), nil
		}
	}
	return domain.SecretVersion{}, domain.ErrNotFound
}

func (r *secretRepository) ListVersions(_ context.Context, secretID int64, limit, offset int) ([]domain.SecretVersion, int, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	all := append([]domain.SecretVersion(nil), r.store.versions[secretID]...)
	sort.Slice(all, func(i, j int) bool { return all[i].Version > all[j].Version })
	// 列表不返回密文。
	meta := make([]domain.SecretVersion, len(all))
	for i, v := range all {
		meta[i] = domain.SecretVersion{
			ID: v.ID, SecretID: v.SecretID, Version: v.Version,
			KEKKeyID: v.KEKKeyID, Algorithm: v.Algorithm, ValueHash: v.ValueHash,
			Status: v.Status, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt,
			RevokedAt: v.RevokedAt, DestroyedAt: v.DestroyedAt,
		}
	}
	total := len(meta)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if limit <= 0 || end > total {
		end = total
	}
	return meta[offset:end], total, nil
}

func (r *secretRepository) UpdateVersionStatus(_ context.Context, secretID, version int64, from, to domain.SecretVersionStatus, at time.Time) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	list := r.store.versions[secretID]
	for i := range list {
		if list[i].Version != version {
			continue
		}
		if list[i].Status != from {
			return domain.ErrStateConflict
		}
		list[i].Status = to
		if to == domain.SecretVersionRevoked {
			list[i].RevokedAt = &at
		}
		if to == domain.SecretVersionDestroyed {
			list[i].DestroyedAt = &at
			list[i].Ciphertext = nil
			list[i].WrappedDEK = nil
			list[i].Nonce = nil
		}
		r.store.versions[secretID] = list
		return nil
	}
	return domain.ErrNotFound
}

func (r *secretRepository) AppendReview(_ context.Context, review domain.SecretReview) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	r.store.reviews[review.SecretID] = append(r.store.reviews[review.SecretID], review)
	return nil
}

func (r *secretRepository) ListReviews(_ context.Context, secretID int64) ([]domain.SecretReview, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	out := append([]domain.SecretReview(nil), r.store.reviews[secretID]...)
	return out, nil
}

type secretGrantRepository struct{ store *secretStore }

func (r *secretGrantRepository) Create(_ context.Context, grant domain.SecretGrant) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	for _, existing := range r.store.grants {
		if existing.Env == grant.Env && existing.Namespace == grant.Namespace &&
			existing.ServiceIdentity == grant.ServiceIdentity && existing.SecretID == grant.SecretID &&
			existing.RevokedAt == nil {
			return domain.ErrDuplicateKey
		}
	}
	r.store.grants[grant.ID] = grant
	return nil
}

func (r *secretGrantRepository) GetByID(_ context.Context, id int64) (domain.SecretGrant, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	g, ok := r.store.grants[id]
	if !ok {
		return domain.SecretGrant{}, domain.ErrNotFound
	}
	return g, nil
}

func (r *secretGrantRepository) ListBySecret(_ context.Context, secretID int64) ([]domain.SecretGrant, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	var out []domain.SecretGrant
	for _, g := range r.store.grants {
		if g.SecretID == secretID {
			out = append(out, g)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (r *secretGrantRepository) FindActive(_ context.Context, env, namespace, serviceIdentity string, secretID int64, now time.Time) (domain.SecretGrant, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	for _, g := range r.store.grants {
		if g.Env == env && g.Namespace == namespace && g.ServiceIdentity == serviceIdentity &&
			g.SecretID == secretID && g.Active(now) {
			return g, nil
		}
	}
	return domain.SecretGrant{}, domain.ErrNotFound
}

func (r *secretGrantRepository) Revoke(_ context.Context, id int64, at time.Time) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	g, ok := r.store.grants[id]
	if !ok {
		return domain.ErrNotFound
	}
	g.RevokedAt = &at
	r.store.grants[id] = g
	return nil
}

type secretAccessRepository struct{ store *secretStore }

func (r *secretAccessRepository) Append(_ context.Context, access domain.SecretAccess) error {
	r.store.mu.Lock()
	defer r.store.mu.Unlock()
	r.store.accesses = append(r.store.accesses, access)
	if len(r.store.accesses) > 1000 {
		r.store.accesses = r.store.accesses[len(r.store.accesses)-1000:]
	}
	return nil
}

func (r *secretAccessRepository) ListBySecretName(_ context.Context, _, _, name string, limit, offset int) ([]domain.SecretAccess, int, error) {
	r.store.mu.RLock()
	defer r.store.mu.RUnlock()
	var matched []domain.SecretAccess
	for i := len(r.store.accesses) - 1; i >= 0; i-- {
		if r.store.accesses[i].SecretName == name {
			matched = append(matched, r.store.accesses[i])
		}
	}
	total := len(matched)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if limit <= 0 || end > total {
		end = total
	}
	return matched[offset:end], total, nil
}

func withFingerprint(s domain.Secret, versions []domain.SecretVersion) domain.Secret {
	out := cloneSecret(s)
	if out.ActiveVersion == nil {
		return out
	}
	for _, v := range versions {
		if v.Version == *out.ActiveVersion {
			out.Fingerprint = v.ValueHash
			break
		}
	}
	return out
}

func cloneSecret(s domain.Secret) domain.Secret {
	out := s
	if s.ActiveVersion != nil {
		v := *s.ActiveVersion
		out.ActiveVersion = &v
	}
	if s.ExpiresAt != nil {
		t := *s.ExpiresAt
		out.ExpiresAt = &t
	}
	return out
}

func cloneSecretVersion(v domain.SecretVersion) domain.SecretVersion {
	out := v
	if len(v.Ciphertext) > 0 {
		out.Ciphertext = append([]byte(nil), v.Ciphertext...)
	}
	if len(v.Nonce) > 0 {
		out.Nonce = append([]byte(nil), v.Nonce...)
	}
	if len(v.WrappedDEK) > 0 {
		out.WrappedDEK = append([]byte(nil), v.WrappedDEK...)
	}
	if v.RevokedAt != nil {
		t := *v.RevokedAt
		out.RevokedAt = &t
	}
	if v.DestroyedAt != nil {
		t := *v.DestroyedAt
		out.DestroyedAt = &t
	}
	return out
}
